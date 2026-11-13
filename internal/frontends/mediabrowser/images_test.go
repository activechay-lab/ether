package mediabrowser

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type imageGrantsFake struct{ issued int }

type imageBatchFake struct {
	imageGrantsFake
	batches int
}

func (f *imageBatchFake) IssueBatch(_ context.Context, request frontend.ImageBatchRequest) ([]frontend.ImageBatchItem, error) {
	if request.Credential != "grant" || len(request.Items) > 50 {
		return nil, errors.New("bad batch")
	}
	f.batches++
	if f.batches == 1 {
		images := []frontend.GrantedImage{{Image: frontend.Image{Kind: frontend.ImagePoster, Width: 600, Height: 300, BlurHash: "gridhash"}, Secret: "scoped-poster"}, {Image: frontend.Image{Kind: frontend.ImageBackdrop}, Secret: "scoped-backdrop"}}
		for _, kind := range request.Kinds {
			if kind == frontend.ImageLogo {
				images = append(images, frontend.GrantedImage{Image: frontend.Image{Kind: frontend.ImageLogo, BlurHash: "logohash"}, Secret: "scoped-logo"})
			}
		}
		return []frontend.ImageBatchItem{{Item: request.Items[0], Authoritative: true, Images: images}, {Item: request.Items[1], Authoritative: false, Images: []frontend.GrantedImage{{Image: frontend.Image{Kind: frontend.ImagePoster}, Secret: "untrusted"}}}}, nil
	}
	return nil, nil
}

func TestGridArtworkUsesBoundedAuthorizedBatches(t *testing.T) {
	grants := &imageBatchFake{}
	i := &instance{protocol: "jellyfin", host: frontend.Host{ImageGrants: grants}}
	keys := make([]frontend.RatingKey, 51)
	for n := range keys {
		keys[n] = frontend.RatingKey(n + 1)
	}
	r := httptest.NewRequest("GET", "/Items", nil)
	r.Header.Set("X-Emby-Token", "grant")
	images := i.gridImages(r, keys)
	if grants.batches != 2 || len(images) != 1 || images[keys[0]]["ImageTags"].(map[string]string)["Primary"] != "scoped-poster" || images[keys[0]]["BackdropImageTags"].([]string)[0] != "scoped-backdrop" {
		t.Fatal(grants.batches, images)
	}
	if images[keys[0]]["PrimaryImageAspectRatio"] != float64(2) || images[keys[0]]["ImageBlurHashes"].(map[string]any)["Primary"].(map[string]string)["scoped-poster"] != "gridhash" {
		t.Fatal(images)
	}
	if images[keys[0]]["ImageTags"].(map[string]string)["Logo"] != "scoped-logo" || images[keys[0]]["ImageBlurHashes"].(map[string]any)["Logo"].(map[string]string)["scoped-logo"] != "logohash" {
		t.Fatal(images)
	}
}

func (f *imageGrantsFake) Issue(_ context.Context, r frontend.ImageGrantRequest) (string, error) {
	if r.Credential != "grant" || r.Item != 5 {
		return "", errors.New("denied")
	}
	f.issued++
	return "scoped-image" + r.Image.Path, nil
}
func (*imageGrantsFake) Revoke(context.Context, string, string) error { return nil }
func (*imageGrantsFake) Serve(w http.ResponseWriter, _ *http.Request, token string) {
	if token != "scoped-image/poster" {
		http.NotFound(w, nil)
		return
	}
	_, _ = w.Write([]byte("image"))
}
func TestImagesRequireScopedGrant(t *testing.T) {
	grants := &imageGrantsFake{}
	ident := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return &frontend.Viewer{ID: 1}, nil }}
	exp := frontendtest.Experience{ItemFunc: func(_ context.Context, r frontend.ItemRequest) (frontend.ItemDetail, error) {
		return frontend.ItemDetail{Title: frontend.Title{Key: r.Key, Type: "movie", Title: "Movie"}, ImagesAuthoritative: true, Images: []frontend.Image{{Kind: "poster", Path: "/poster", Width: 400, Height: 200, BlurHash: "blurhash-primary"}, {Kind: "poster", Path: "/alternate", Width: 3, Height: 4, BlurHash: "alternate"}}}, nil
	}}
	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{Identity: ident, Experience: exp, ImageGrants: grants})
	if err != nil {
		t.Fatal(err)
	}
	id := ids.RatingKeyGUID(5)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/Items/"+id, nil)
	r.Header.Set("X-Emby-Token", "grant")
	inst.Handler().ServeHTTP(w, r)
	if w.Code != 200 || grants.issued != 1 || !strings.Contains(w.Body.String(), "scoped-image/poster") {
		t.Fatal(w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"PrimaryImageAspectRatio":2`) || !strings.Contains(w.Body.String(), `"ImageBlurHashes":{"Primary":{"scoped-image/poster":"blurhash-primary"}}`) || strings.Contains(w.Body.String(), "alternate") {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	inst.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/Items/"+id+"/Images/Primary", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	inst.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/Items/"+id+"/Images/Primary?tag=scoped-image%2Fposter", nil))
	if w.Code != 200 || w.Body.String() != "image" {
		t.Fatal(w.Code, w.Body.String())
	}
}

// Issue #405: Jellyfin makes `tag` optional and some TV clients (Moonfin)
// omit it. A tag-less request carrying the client's own credential is
// served by minting the same scoped grant in-process; anonymous tag-less
// requests, and tag-less requests for items the viewer may not see, stay
// an indistinguishable 404.
func TestTaglessImageServesOnlyForAuthenticatedViewer(t *testing.T) {
	grants := &imageGrantsFake{}
	ident := frontendtest.Identity{ResolveFunc: func(_ context.Context, token string) (*frontend.Viewer, error) {
		if token != "grant" {
			return nil, errors.New("bad token")
		}
		return &frontend.Viewer{ID: 1}, nil
	}}
	exp := frontendtest.Experience{ItemFunc: func(_ context.Context, r frontend.ItemRequest) (frontend.ItemDetail, error) {
		return frontend.ItemDetail{Title: frontend.Title{Key: r.Key, Type: "movie", Title: "Movie"}, ImagesAuthoritative: true, Images: []frontend.Image{{Kind: "poster", Path: "/poster"}}}, nil
	}}
	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{Identity: ident, Experience: exp, ImageGrants: grants})
	if err != nil {
		t.Fatal(err)
	}
	id := ids.RatingKeyGUID(5)
	hidden := ids.RatingKeyGUID(6) // the grant fake denies every item but 5
	cases := []struct {
		name  string
		url   string
		token string
		want  int
	}{
		{"authenticated no tag", "/Items/" + id + "/Images/Primary", "grant", 200},
		{"authenticated no tag indexed", "/Items/" + id + "/Images/Primary/0", "grant", 200},
		{"authenticated empty tag", "/Items/" + id + "/Images/Primary?tag=", "grant", 200},
		{"api_key credential", "/Items/" + id + "/Images/Primary?api_key=grant", "", 200},
		{"anonymous no tag", "/Items/" + id + "/Images/Primary", "", 404},
		{"bad credential no tag", "/Items/" + id + "/Images/Primary", "nope", 404},
		{"hidden item no tag", "/Items/" + hidden + "/Images/Primary", "grant", 404},
		{"missing kind no tag", "/Items/" + id + "/Images/Backdrop", "grant", 404},
		{"anonymous with forged tag", "/Items/" + id + "/Images/Primary?tag=forged", "", 404},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", tc.url, nil)
		if tc.token != "" {
			r.Header.Set("X-Emby-Token", tc.token)
		}
		inst.Handler().ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s: got %d want %d (%s)", tc.name, w.Code, tc.want, w.Body.String())
		}
		if tc.want == 200 && w.Body.String() != "image" {
			t.Fatalf("%s: body %q", tc.name, w.Body.String())
		}
	}
}

type publicExperience struct {
	frontendtest.Experience
}

func (publicExperience) PublicArtwork(_ context.Context, _ frontend.Device, item frontend.RatingKey) ([]frontend.Image, *frontend.Viewer, bool, error) {
	if item != 5 {
		return nil, nil, false, nil
	}
	return []frontend.Image{{Kind: "poster", Path: "/poster"}}, &frontend.Viewer{ID: 1}, true, nil
}

type publicGrantsFake struct {
	imageGrantsFake
}

func (*publicGrantsFake) ServePublic(w http.ResponseWriter, r *http.Request, _ frontend.Device, item frontend.RatingKey, img frontend.Image) {
	if item != 5 || img.Path != "/poster" {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write([]byte("image"))
}

// Issue #405, anonymous half: Moonfin loads posters with no tag and no
// credential at all. Those requests serve only title artwork the
// experience reports as visible to every viewer; a forged tag never
// falls through to the public path.
func TestAnonymousTaglessImageServesOnlyPublicArtwork(t *testing.T) {
	exp := publicExperience{Experience: frontendtest.Experience{ItemFunc: func(_ context.Context, r frontend.ItemRequest) (frontend.ItemDetail, error) {
		return frontend.ItemDetail{Title: frontend.Title{Key: r.Key, Type: "movie", Title: "Movie"}, ImagesAuthoritative: true, Images: []frontend.Image{{Kind: "poster", Path: "/poster"}}}, nil
	}}}
	ident := frontendtest.Identity{ResolveFunc: func(_ context.Context, token string) (*frontend.Viewer, error) {
		if token != "grant" {
			return nil, errors.New("bad token")
		}
		return &frontend.Viewer{ID: 1}, nil
	}}
	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{Identity: ident, Experience: exp, ImageGrants: &publicGrantsFake{}})
	if err != nil {
		t.Fatal(err)
	}
	id, hidden := ids.RatingKeyGUID(5), ids.RatingKeyGUID(6)
	cases := []struct {
		name string
		url  string
		want int
	}{
		{"anonymous public poster", "/Items/" + id + "/Images/Primary", 200},
		{"anonymous public poster indexed", "/Items/" + id + "/Images/Primary/0", 200},
		{"anonymous hidden poster", "/Items/" + hidden + "/Images/Primary", 404},
		{"anonymous missing kind", "/Items/" + id + "/Images/Backdrop", 404},
		{"anonymous forged tag stays denied", "/Items/" + id + "/Images/Primary?tag=forged", 404},
		{"anonymous unresolvable id", "/Items/not-a-guid/Images/Primary", 404},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		inst.Handler().ServeHTTP(w, httptest.NewRequest("GET", tc.url, nil))
		if w.Code != tc.want {
			t.Fatalf("%s: got %d want %d (%s)", tc.name, w.Code, tc.want, w.Body.String())
		}
		if tc.want == 200 && w.Body.String() != "image" {
			t.Fatalf("%s: body %q", tc.name, w.Body.String())
		}
	}
}

func TestStillIsPrimaryWhenPosterIsAbsent(t *testing.T) {
	grants := &imageGrantsFake{}
	i := &instance{protocol: "jellyfin", host: frontend.Host{ImageGrants: grants}}
	r := httptest.NewRequest("GET", "/Items/x", nil)
	r.Header.Set("X-Emby-Token", "grant")
	still := frontend.ItemDetail{Title: frontend.Title{Key: 5, Type: "episode"}, ImagesAuthoritative: true, Images: []frontend.Image{{Kind: "still", Path: "/still", Width: 400, Height: 200, BlurHash: "stillhash"}}}
	tags, _, meta := i.imageTags(r, still, "grant")
	if tags["Primary"] != "scoped-image/still" || tags["Thumb"] != nil {
		t.Fatal(tags)
	}
	hashes, _ := meta["ImageBlurHashes"].(map[string]any)
	primary, _ := hashes["Primary"].(map[string]string)
	if meta["PrimaryImageAspectRatio"] != float64(2) || primary["scoped-image/still"] != "stillhash" {
		t.Fatal(meta)
	}
	both := frontend.ItemDetail{Title: frontend.Title{Key: 5, Type: "episode"}, ImagesAuthoritative: true, Images: []frontend.Image{{Kind: "still", Path: "/still"}, {Kind: "poster", Path: "/poster"}}}
	tags, _, _ = i.imageTags(r, both, "grant")
	if tags["Primary"] != "scoped-image/poster" || tags["Thumb"] != "scoped-image/still" {
		t.Fatal(tags)
	}
}

func TestImageMetadataRequiresGrantedAndKnownDescriptor(t *testing.T) {
	i := &instance{protocol: "jellyfin"}
	dto := map[string]any{}
	i.addImageMetadata(dto, "Primary", "", frontend.Image{Width: 400, Height: 200, BlurHash: "secret"})
	if len(dto) != 0 {
		t.Fatal(dto)
	}
	i.addImageMetadata(dto, "Primary", "grant", frontend.Image{})
	if len(dto) != 0 {
		t.Fatal(dto)
	}
	emby := &instance{protocol: "emby"}
	emby.addImageMetadata(dto, "Primary", "grant", frontend.Image{Width: 400, Height: 200, BlurHash: "hidden"})
	if dto["PrimaryImageAspectRatio"] != float64(2) || dto["ImageBlurHashes"] != nil {
		t.Fatal(dto)
	}
}

func TestItemImagesRouteShape(t *testing.T) {
	for _, protocol := range []string{"jellyfin", "emby"} {
		t.Run(protocol, func(t *testing.T) {
			ident := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return &frontend.Viewer{ID: 1}, nil }}
			exp := frontendtest.Experience{ItemFunc: func(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
				if req.Key != 5 {
					return frontend.ItemDetail{}, frontend.ErrNotFound
				}
				return frontend.ItemDetail{Title: frontend.Title{Key: 5}, ImagesAuthoritative: true, Images: []frontend.Image{
					{Kind: "poster", Path: "/poster"}, {Kind: "backdrop", Path: "/backdrop"}, {Kind: "backdrop", Path: "/backdrop2"},
					{Kind: "logo", Path: "/logo"}, {Kind: "thumb", Path: "/thumb"}, {Kind: "banner", Path: "/banner"},
				}}, nil
			}}
			inst, err := mount(context.Background(), frontend.Host{Identity: ident, Experience: exp, ImageGrants: &imageGrantsFake{}}, "test", frontend.Protocol(protocol))
			if err != nil {
				t.Fatal(err)
			}
			defer inst.Close(context.Background())
			call := func(path string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("GET", path, nil)
				r.Header.Set("X-Emby-Token", "grant")
				w := httptest.NewRecorder()
				inst.Handler().ServeHTTP(w, r)
				return w
			}
			w := call("/" + protocol + "/iTeMs/" + ids.RatingKeyGUID(5) + "/iMaGeS")
			var images []struct {
				ImageType  string
				ImageTag   string
				ImageIndex *int
				Size       int64
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &images) != nil || len(images) != 6 {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			expected := []string{"Primary", "Logo", "Thumb", "Banner", "Backdrop", "Backdrop"}
			for n, img := range images {
				if img.ImageType != expected[n] || !strings.HasPrefix(img.ImageTag, "scoped-image/") {
					t.Fatalf("%+v", img)
				}
				if n >= 4 && (img.ImageIndex == nil || *img.ImageIndex != n-4) {
					t.Fatalf("%+v", img)
				}
			}
			for _, path := range []string{"/Items/" + ids.RatingKeyGUID(6) + "/Images", "/Items/invalid/Images", "/Unimplemented/Endpoint"} {
				w := call(path)
				var problem struct {
					Status int
					Title  string
				}
				if w.Code != 404 || json.Unmarshal(w.Body.Bytes(), &problem) != nil || problem.Status != 404 || problem.Title == "" {
					t.Fatalf("%d %s", w.Code, w.Body.String())
				}
			}
		})
	}
}
