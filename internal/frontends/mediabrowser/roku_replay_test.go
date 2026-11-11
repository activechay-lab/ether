package mediabrowser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

// rokuLibrary is an offline Jellyfin library holding one film, one series with
// one season and two episodes, and resume state, so Jellyfin for Roku 3.2.4's
// request sequence can be replayed without a live server (#421).
type rokuLibrary struct {
	frontendtest.Experience
	movie   frontend.Title
	show    frontend.Title
	season  frontend.Title
	eps     []frontend.Title
	resumed map[frontend.RatingKey]core.WatchStatus
}

func (l *rokuLibrary) byKey(key frontend.RatingKey) (frontend.Title, bool) {
	switch key {
	case l.movie.Key:
		return l.movie, true
	case l.show.Key:
		return l.show, true
	case l.season.Key:
		return l.season, true
	}
	for _, ep := range l.eps {
		if ep.Key == key {
			return ep, true
		}
	}
	return frontend.Title{}, false
}

func (l *rokuLibrary) item(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
	t, ok := l.byKey(req.Key)
	if !ok {
		return frontend.ItemDetail{}, frontend.ErrNotFound
	}
	detail := frontend.ItemDetail{Title: t}
	if t.Type == "movie" || t.Type == "episode" {
		detail.ImagesAuthoritative = true
		detail.Tracks = []frontend.StreamInfo{
			{Type: "video", Index: 0, Codec: "h264", Width: 1920, Height: 1080},
			{Type: "audio", Index: 1, Codec: "aac", Channels: 2},
		}
		detail.Images = []frontend.Image{
			{Kind: frontend.ImagePoster, Path: "poster.jpg", Tag: "t-" + ids.RatingKeyGUID(t.Key)},
			{Kind: frontend.ImageBackdrop, Path: "backdrop.jpg", Tag: "b-" + ids.RatingKeyGUID(t.Key)},
		}
	}
	return detail, nil
}

// LatestAdded and Resume make rokuLibrary a frontend.LibraryActivity, so the
// resume route answers from this library rather than reporting unavailable.
func (l *rokuLibrary) LatestAdded(context.Context, *frontend.Viewer, frontend.Device, string, int) ([]frontend.LatestItem, error) {
	return nil, nil
}
func (l *rokuLibrary) Resume(_ context.Context, _ *frontend.Viewer, _ frontend.Device, _ string, _ []frontend.MediaType, _, _ int, _ bool) (frontend.ActivityPage, error) {
	return frontend.ActivityPage{Items: []frontend.Title{l.eps[0]}, Total: 1, TotalExact: true}, nil
}

// showChildren answers a recursive episode listing and a non-recursive season
// listing, as the real experience does.
func (l *rokuLibrary) childrenForTypes(_ context.Context, _ *frontend.Viewer, _ frontend.Device, key frontend.RatingKey, q frontend.ItemQuery) (core.Page, error) {
	switch key {
	case l.show.Key:
		if q.Recursive {
			return core.Page{Items: l.eps, Total: len(l.eps)}, nil
		}
		return core.Page{Items: []frontend.Title{l.season}, Total: 1}, nil
	case l.season.Key:
		return core.Page{Items: l.eps, Total: len(l.eps)}, nil
	}
	return core.Page{}, nil
}

func (l *rokuLibrary) userData(_ context.Context, _ *frontend.Viewer, keys []core.RatingKey) (map[core.RatingKey]core.WatchStatus, error) {
	out := map[core.RatingKey]core.WatchStatus{}
	for _, key := range keys {
		if st, ok := l.resumed[key]; ok {
			out[key] = st
		}
	}
	return out, nil
}

// rokuImages mints a secret for every image and serves each secret it minted,
// so a test can follow an advertised tag and require it to resolve.
type rokuImages struct {
	issued map[string]bool
}

func (g *rokuImages) Issue(_ context.Context, req frontend.ImageGrantRequest) (string, error) {
	secret := "img:" + ids.RatingKeyGUID(req.Item) + ":" + req.Image.Kind
	if g.issued == nil {
		g.issued = map[string]bool{}
	}
	g.issued[secret] = true
	return secret, nil
}
func (g *rokuImages) Revoke(context.Context, string, string) error { return nil }
func (g *rokuImages) Serve(w http.ResponseWriter, _ *http.Request, secret string) {
	if !g.issued[secret] {
		http.NotFound(w, nil)
		return
	}
	w.WriteHeader(http.StatusOK)
}
func (g *rokuImages) IssueBatch(_ context.Context, req frontend.ImageBatchRequest) ([]frontend.ImageBatchItem, error) {
	return g.sign(req.Items, nil), nil
}
func (g *rokuImages) SignPage(_ context.Context, req frontend.ImagePageRequest) ([]frontend.ImageBatchItem, error) {
	items := make([]core.RatingKey, 0, len(req.Titles))
	for _, t := range req.Titles {
		items = append(items, t.Key)
	}
	return g.sign(items, rokImageFor), nil
}
func (g *rokuImages) sign(keys []core.RatingKey, image func(core.RatingKey) []frontend.Image) []frontend.ImageBatchItem {
	out := make([]frontend.ImageBatchItem, 0, len(keys))
	for _, key := range keys {
		images := []frontend.Image{}
		if image != nil {
			images = image(key)
		}
		item := frontend.ImageBatchItem{Item: key, Authoritative: true}
		for _, img := range images {
			secret, _ := g.Issue(context.Background(), frontend.ImageGrantRequest{Item: key, Image: img})
			item.Images = append(item.Images, frontend.GrantedImage{Image: img, Secret: secret})
		}
		out = append(out, item)
	}
	return out
}
func rokImageFor(key core.RatingKey) []frontend.Image {
	return []frontend.Image{{Kind: frontend.ImagePoster, Path: "poster.jpg", Tag: "t-" + ids.RatingKeyGUID(key)}}
}

// mountRoku wires a Jellyfin surface over the offline library.
func mountRoku(t *testing.T) (*rokuLibrary, *rokuImages, frontend.Instance) {
	t.Helper()
	viewer := &frontend.Viewer{ID: 1, Username: "viewer"}
	lib := &rokuLibrary{
		movie: frontend.Title{Key: core.MovieKey(11), Type: "movie", Title: "Film", Year: 2020, Runtime: 90 * time.Minute, Genres: []string{"Drama"}},
		show:  frontend.Title{Key: core.ShowKey(42), Type: "show", Title: "Corrie", ChildCount: 1, LeafCount: 2, Genres: []string{"Soap"}},
		eps: []frontend.Title{
			{Key: core.EpisodeKey(42, 1, 1), Type: "episode", Title: "Ep1", ShowKey: core.ShowKey(42), ShowTitle: "Corrie", Season: 1, Episode: 1, Genres: []string{"Soap"}},
			{Key: core.EpisodeKey(42, 1, 2), Type: "episode", Title: "Ep2", ShowKey: core.ShowKey(42), ShowTitle: "Corrie", Season: 1, Episode: 2, Genres: []string{"Soap"}},
		},
		resumed: map[core.RatingKey]core.WatchStatus{core.EpisodeKey(42, 1, 1): {ViewOffset: 5 * time.Minute, Duration: 40 * time.Minute}},
	}
	lib.season = frontend.Title{Key: core.SeasonKey(42, 1), Type: "season", Title: "Season 1", ShowKey: core.ShowKey(42), ShowTitle: "Corrie", Season: 1}
	lib.ItemFunc = lib.item
	lib.ChildrenFunc = lib.childrenForTypes
	lib.UserDataFunc = lib.userData
	images := &rokuImages{}
	ident := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}
	inst, err := (Jellyfin{MachineID: "roku"}).Mount(context.Background(), frontend.Host{Identity: ident, Experience: lib, ImageGrants: images})
	if err != nil {
		t.Fatal(err)
	}
	return lib, images, inst
}

func rokuGet(t *testing.T, inst frontend.Instance, path string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("X-Emby-Token", "grant")
	inst.Handler().ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

func rokuJSON(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("not JSON: %v: %s", err, body)
	}
	return m
}

func itemsOf(t *testing.T, body string) []map[string]any {
	t.Helper()
	m := rokuJSON(t, body)
	raw, _ := m["Items"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		item, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("item is not an object: %v", entry)
		}
		out = append(out, item)
	}
	return out
}

// TestRokuFilmDetailsReplay replays Jellyfin for Roku 3.2.4 opening a film:
// the item metadata call it makes, plus local trailers and additional parts,
// then requires the fields its MovieDetails scene dereferences.
func TestRokuFilmDetailsReplay(t *testing.T) {
	lib, _, inst := mountRoku(t)
	id := ids.RatingKeyGUID(lib.movie.Key)

	code, body := rokuGet(t, inst, "/Items/"+id+"?fields=Chapters,Trickplay")
	if code != 200 {
		t.Fatalf("film detail: %d %s", code, body)
	}
	detail := rokuJSON(t, body)
	for _, key := range []string{"Id", "Name", "Type", "MediaSources", "MediaStreams", "People", "Genres", "UserData", "Chapters", "Trickplay", "ImageTags", "ProviderIds"} {
		if _, ok := detail[key]; !ok {
			t.Errorf("film detail missing %q", key)
		}
	}
	if detail["Type"] != "Movie" {
		t.Errorf("film Type=%v", detail["Type"])
	}
	// LocalTrailers and AdditionalParts must answer, not 404: the Roku aborts
	// the details scene when either fails.
	for _, path := range []string{"/Items/" + id + "/LocalTrailers", "/Videos/" + id + "/AdditionalParts"} {
		if code, body := rokuGet(t, inst, path); code != 200 {
			t.Errorf("%s: %d %s", path, code, body)
		}
	}
}

// TestRokuSeriesToEpisodeReplay replays the Roku sequence series -> season ->
// episode -> play and requires SeriesId and SeasonId on every season and
// episode row, from every listing path the app uses (Seasons, Episodes,
// Resume, NextUp, Items). A row without SeriesId is what makes the app build
// /Shows//episodes and exit (#421).
func TestRokuSeriesToEpisodeReplay(t *testing.T) {
	lib, _, inst := mountRoku(t)
	showID := ids.RatingKeyGUID(lib.show.Key)
	seasonID := ids.RatingKeyGUID(lib.season.Key)
	epID := ids.RatingKeyGUID(lib.eps[0].Key)

	listings := map[string][]map[string]any{}
	for name, path := range map[string]string{
		"Seasons":        "/Shows/" + showID + "/Seasons",
		"Episodes":       "/Shows/" + showID + "/Episodes?seasonId=" + seasonID,
		"Resume":         "/UserItems/Resume",
		"ResumeBySeries": "/UserItems/Resume?parentId=" + showID + "&Filters=IsResumable&recursive=true",
		"NextUp":         "/Shows/NextUp?seriesId=" + showID,
		"Items":          "/Items?parentId=" + showID + "&includeitemtypes=Episode&recursive=true",
		"ItemsIncSeas":   "/Items?parentId=" + showID + "&includeitemtypes=Season,Episode&recursive=true",
		"SeasonItems":    "/Items/" + seasonID,
	} {
		code, body := rokuGet(t, inst, path)
		if code != 200 {
			t.Errorf("%s (%s): %d %s", name, path, code, body)
			continue
		}
		listings[name] = itemsOf(t, body)
	}

	assertSeriesIdentity := func(listing string, items []map[string]any) {
		for _, item := range items {
			typ, _ := item["Type"].(string)
			switch typ {
			case "Season", "Episode":
				if s, _ := item["SeriesId"].(string); s == "" {
					t.Errorf("%s: %s %v missing SeriesId", listing, typ, item["Name"])
				} else if _, err := ids.ParseRatingKeyGUID(s); err != nil {
					t.Errorf("%s: %s SeriesId %q is not a rating-key GUID", listing, typ, s)
				}
			}
			if typ == "Episode" {
				if s, _ := item["SeasonId"].(string); s == "" {
					t.Errorf("%s: Episode %v missing SeasonId", listing, item["Name"])
				} else if _, err := ids.ParseRatingKeyGUID(s); err != nil {
					t.Errorf("%s: Episode SeasonId %q is not a rating-key GUID", listing, s)
				}
				if item["ParentIndexNumber"] == nil || item["IndexNumber"] == nil {
					t.Errorf("%s: Episode %v missing ParentIndexNumber/IndexNumber", listing, item["Name"])
				}
			}
		}
	}
	for name, items := range listings {
		if len(items) == 0 && (name == "Seasons" || name == "Episodes" || name == "ResumeBySeries") {
			t.Errorf("%s: expected at least one row", name)
		}
		assertSeriesIdentity(name, items)
	}

	// The episode the app plays must expose media for playback.
	code, body := rokuGet(t, inst, "/Items/"+epID+"?fields=MediaStreams,MediaSources,Overview")
	if code != 200 {
		t.Fatalf("episode detail: %d %s", code, body)
	}
	ep := rokuJSON(t, body)
	for _, key := range []string{"SeriesId", "SeasonId", "MediaSources", "MediaStreams", "ParentIndexNumber", "IndexNumber"} {
		if ep[key] == nil {
			t.Errorf("episode detail missing %q", key)
		}
	}
}

// TestRokuAdvertisedImagesResolve requires that every image tag the server
// advertises on a film and an episode resolves to a served image: ImageTags
// only advertise images we actually serve (#421 image 404s).
func TestRokuAdvertisedImagesResolve(t *testing.T) {
	lib, _, inst := mountRoku(t)
	for _, key := range []core.RatingKey{lib.movie.Key, lib.eps[0].Key} {
		id := ids.RatingKeyGUID(key)
		code, body := rokuGet(t, inst, "/Items/"+id+"?fields=MediaStreams")
		if code != 200 {
			t.Fatalf("detail %s: %d", id, code)
		}
		detail := rokuJSON(t, body)
		tags, _ := detail["ImageTags"].(map[string]any)
		primary, _ := tags["Primary"].(string)
		if primary == "" {
			t.Fatalf("item %s advertises no Primary tag", id)
		}
		code, _ = rokuGet(t, inst, "/Items/"+id+"/Images/Primary?tag="+primary)
		if code != 200 {
			t.Errorf("item %s advertised Primary tag %q but /Images/Primary answered %d", id, primary, code)
		}
	}
	// An episode with no poster of its own is drawn from the series it
	// inherits from: the app requests the series image with SeriesId and
	// SeriesPrimaryImageTag, so both must resolve.
	id := ids.RatingKeyGUID(lib.eps[0].Key)
	_, body := rokuGet(t, inst, "/Items/"+id+"?fields=MediaStreams")
	detail := rokuJSON(t, body)
	via := func(ids, tag any) {
		sid, _ := ids.(string)
		st, _ := tag.(string)
		if sid == "" || st == "" {
			t.Errorf("episode advertises no inherited series image (id=%v tag=%v)", ids, tag)
			return
		}
		if code, body := rokuGet(t, inst, "/Items/"+sid+"/Images/Primary?tag="+st); code != 200 {
			t.Errorf("episode inherited series image %s?tag=%s answered %d %s", sid, st, code, body)
		}
	}
	via(detail["SeriesId"], detail["SeriesPrimaryImageTag"])
	via(detail["ParentPrimaryImageItemId"], detail["ParentPrimaryImageTag"])
}

// TestAndroidTVSignInReplay replays Jellyfin for Android TV's sign-in: the
// AuthenticateByName body shape + headers, then the Quick Connect
// Initiate/Connect/AuthenticateWithQuickConnect flow, requiring the fields
// jellyfin-sdk-kotlin dereferences (non-null without a default) on
// AuthenticationResult and UserDto. A missing one fails the whole response,
// and sign-in then ends with "Invalid username and/or password" (#421).
func TestAndroidTVSignInReplay(t *testing.T) {
	const token, secret = "android-token", "android-secret"
	viewer := &frontend.Viewer{ID: 7, Username: "viewer"}
	identity := frontendtest.Identity{
		ResolveFunc: func(_ context.Context, credential string) (*frontend.Viewer, error) {
			if credential != token {
				return nil, frontend.ErrIdentityUnavailable
			}
			return viewer, nil
		},
		SignInFunc: func(_ context.Context, user, password string, _ frontend.Device) (string, error) {
			if user != "viewer" || password != "password" {
				return "", core.ErrUnauthorized
			}
			return token, nil
		},
		InitiateFunc: func(context.Context, frontend.Device) (string, string, error) {
			return "654321", "android-pending", nil
		},
		DetailsFunc: func(_ context.Context, s string) (frontend.QuickConnectDetails, error) {
			return frontend.QuickConnectDetails{Approved: s == secret, Code: "654321", Secret: s, Device: frontend.Device{ID: "shield", Client: "Jellyfin Android TV", DeviceName: "SHIELD"}}, nil
		},
		PollFunc: func(_ context.Context, s string) (string, error) {
			if s != secret {
				return "", frontend.ErrIdentityUnavailable
			}
			return token, nil
		},
		StatusFunc: func(_ context.Context, s string) (bool, error) { return s == secret, nil },
	}
	exp := &frontendtest.Experience{}
	inst, err := (Jellyfin{MachineID: "androidtv"}).Mount(context.Background(), frontend.Host{Identity: identity, Experience: exp, Sessions: frontendtest.Sessions{}})
	if err != nil {
		t.Fatal(err)
	}
	post := func(path, body string) (int, string) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Jellyfin Android TV", Device="SHIELD", DeviceId="shield", Version="0.18.2"`)
		inst.Handler().ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	code, body := post("/Users/AuthenticateByName", `{"Username":"viewer","Pw":"password"}`)
	if code != 200 {
		t.Fatalf("AuthenticateByName: %d %s", code, body)
	}
	result := rokuJSON(t, body)
	if result["AccessToken"] != token {
		t.Errorf("AccessToken = %v", result["AccessToken"])
	}
	user, _ := result["User"].(map[string]any)
	if user == nil {
		t.Fatalf("no User in %s", body)
	}
	for _, key := range []string{"Id", "Name", "ServerId", "HasPassword", "Policy", "Configuration"} {
		if user[key] == nil {
			t.Errorf("User missing %q", key)
		}
	}
	policy, _ := user["Policy"].(map[string]any)
	for _, key := range []string{"IsAdministrator", "IsHidden", "IsDisabled", "EnableMediaPlayback", "EnableAllFolders", "EnableAllDevices", "AuthenticationProviderId"} {
		if _, ok := policy[key]; !ok {
			t.Errorf("Policy missing %q", key)
		}
	}
	if _, ok := result["SessionInfo"]; !ok {
		t.Errorf("AuthenticationResult missing SessionInfo")
	}

	// Quick Connect: Initiate returns a code + secret, Connect reports the
	// approval, and AuthenticateWithQuickConnect redeems the secret.
	code, body = post("/QuickConnect/Initiate", "")
	if code != 200 {
		t.Fatalf("QuickConnect/Initiate: %d %s", code, body)
	}
	initiate := rokuJSON(t, body)
	if initiate["Code"] == "" || initiate["Secret"] == "" || initiate["Authenticated"] != false {
		t.Errorf("Initiate = %v", initiate)
	}
	code, body = rokuGet(t, inst, "/QuickConnect/Connect?secret="+secret)
	if code != 200 || rokuJSON(t, body)["Authenticated"] != true {
		t.Errorf("Connect: %d %s", code, body)
	}
	code, body = post("/Users/AuthenticateWithQuickConnect", `{"Secret":"`+secret+`"}`)
	if code != 200 || rokuJSON(t, body)["AccessToken"] != token {
		t.Errorf("AuthenticateWithQuickConnect: %d %s", code, body)
	}
}
