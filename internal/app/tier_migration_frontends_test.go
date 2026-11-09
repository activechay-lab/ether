package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/compactids"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/plextv"
)

// TestTierMigratedKeysOpenOnEveryFrontend: a client that held a title's
// id before the tier migration removed it (a Plex rating key, a Jellyfin,
// Emby or Silo item id, a Stremio tmdb id, an Xtream compact id, a DLNA
// object id, the console's key) opens it afterwards: the frontend resolves
// the id to the same rating key, catalog.Get fetches the title from TMDB
// once and stores it again (promotion), and the response is the title.
func TestTierMigratedKeysOpenOnEveryFrontend(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = cfg.Update(func(c *config.Config) error {
		c.SetupComplete = true
		c.Plex.Token = "test-owner"
		c.Transcode.FFmpegPath = "/not-installed/ffmpeg"
		c.Frontends = map[string]config.Frontend{
			"plex":     {Enabled: true},
			"jellyfin": {Enabled: true, Settings: map[string]any{"port": float64(18196), "discovery": false}},
			"emby":     {Enabled: true, Settings: map[string]any{"port": float64(18197), "discovery": false}},
			"silo":     {Enabled: true, Settings: map[string]any{"port": float64(18190), "discovery": false}},
			"stremio":  {Enabled: true},
			"xtream":   {Enabled: true},
			"dlna":     {Enabled: true, Settings: map[string]any{"discovery": false}},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	a, err := New(ctx, Options{
		DataDir:    dir,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		plextvHTTP: &http.Client{Transport: fakePlexTVTransport{}},
		diskUsage:  func() (int64, int64, error) { return 1 << 40, 1 << 39, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	if err = a.users.SetOwner(ctx, plextv.Account{ID: 42, Username: "owner"}, "test-owner"); err != nil {
		t.Fatal(err)
	}
	owner, err := a.users.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fake := &tierTMDB{}
	a.catalog.SetTMDB(fake)

	// A long-tail title per frontend, as an Off mirror stored it, and one
	// library title so the mirror is not empty after the migration.
	surfaces := []string{"plex", "jellyfin", "emby", "silo", "stremio", "xtream", "dlna", "console"}
	keys := map[string]core.RatingKey{}
	released := time.Date(2001, 2, 3, 0, 0, 0, 0, time.UTC)
	for i, s := range surfaces {
		id := int64(7001 + i)
		keys[s] = core.MovieKey(id)
		if err := a.catalog.UpsertTitle(ctx, core.Title{Key: keys[s], Type: core.TypeMovie, IDs: core.IDs{TMDB: id}, Title: fmt.Sprintf("Tail %d", id), Released: released, Popularity: 0.2, VoteCount: 1}, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.catalog.UpsertTitle(ctx, core.Title{Key: core.MovieKey(603), Type: core.TypeMovie, IDs: core.IDs{TMDB: 603}, Title: "Library", Released: released, Popularity: 80, VoteCount: 20000}, true); err != nil {
		t.Fatal(err)
	}
	compact, err := compactids.New(a.db)
	if err != nil {
		t.Fatal(err)
	}
	xtreamID, err := compact.Compact(ctx, "xtream", "item", fmt.Sprint(int64(keys["xtream"])))
	if err != nil {
		t.Fatal(err)
	}

	// The upgrade: armed, backed up, migrated.
	if _, err := a.db.ExecContext(ctx, `INSERT INTO catalog_meta(k,v) VALUES('tier_migrate_armed',?)`, strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10)); err != nil {
		t.Fatal(err)
	}
	a.catalog.SetTierBackup(func(context.Context, time.Time) (string, error) { return "ether-test.tar.gz", nil })
	if err := a.catalog.MigrateTiers(ctx); err != nil {
		t.Fatal(err)
	}
	stored := func(k core.RatingKey) bool {
		var n int
		if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_titles WHERE key=?`, int64(k)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	for s, k := range keys {
		if stored(k) {
			t.Fatalf("%s's title %d survived the migration; the test needs it removed", s, k)
		}
	}

	if err = a.frontends.control.PutSettings(ctx, "dlna", frontend.Settings{"viewerId": float64(owner.ID), "discovery": false}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Handler())
	t.Cleanup(server.Close)
	a.frontends.start(ctx, server.Listener.Addr())
	t.Cleanup(a.frontends.stop)
	grants, err := identity.NewGrants(a.db, a.users, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{}
	for _, p := range []string{"jellyfin", "emby", "silo", "stremio", "xtream"} {
		if tokens[p], err = grants.Issue(ctx, owner.ID, nil, p, core.Device{ID: "device-" + p}); err != nil {
			t.Fatal(err)
		}
	}
	userGUID, err := ids.GUID(ids.User, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	plexHeader := map[string]string{"X-Plex-Token": "test-owner", "X-Ether-Request": "1", "Accept": "application/json"}
	jf := map[string]string{"Authorization": `MediaBrowser Client="TV", Device="Living Room", DeviceId="device-jellyfin", Version="1", Token="` + tokens["jellyfin"] + `"`}
	em := map[string]string{"X-Emby-Token": tokens["emby"], "X-Emby-Device-Id": "device-emby"}
	silo := map[string]string{"Authorization": "Bearer " + tokens["silo"], "X-Silo-Device-Id": "device-silo", "X-Silo-Client": "tv"}
	soap := func(k core.RatingKey) string {
		return `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:Browse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1"><ObjectID>item:` + strconv.FormatInt(int64(k), 10) + `</ObjectID><BrowseFlag>BrowseMetadata</BrowseFlag><Filter>*</Filter><StartingIndex>0</StartingIndex><RequestedCount>1</RequestedCount><SortCriteria></SortCriteria></u:Browse></s:Body></s:Envelope>`
	}
	type open struct {
		method, path, body string
		header             map[string]string
	}
	opens := map[string]open{
		"plex":     {http.MethodGet, "/library/metadata/" + keys["plex"].String(), "", plexHeader},
		"jellyfin": {http.MethodGet, "/jellyfin/Items/" + ids.RatingKeyGUID(keys["jellyfin"]), "", jf},
		"emby":     {http.MethodGet, "/emby/Users/" + userGUID + "/Items/" + ids.RatingKeyGUID(keys["emby"]), "", em},
		"silo":     {http.MethodGet, "/silo/api/v2/catalog/items/" + ids.RatingKeyGUID(keys["silo"]), "", silo},
		"stremio":  {http.MethodGet, fmt.Sprintf("/stremio/%s/meta/movie/tmdb:%d.json", tokens["stremio"], keys["stremio"].TMDB()), "", nil},
		"xtream":   {http.MethodGet, fmt.Sprintf("/xtream/player_api.php?username=%s&password=%s&action=get_vod_info&vod_id=%d", frontend.XtreamUsername(tokens["xtream"]), tokens["xtream"], xtreamID), "", nil},
		"dlna":     {http.MethodPost, "/dlna/ContentDirectory/control", soap(keys["dlna"]), map[string]string{"SOAPACTION": `"urn:schemas-upnp-org:service:ContentDirectory:1#Browse"`, "Content-Type": "text/xml"}},
		"console":  {http.MethodGet, "/ether/api/catalog/title/" + keys["console"].String(), "", plexHeader},
	}
	client := server.Client()
	for _, s := range surfaces {
		t.Run(s, func(t *testing.T) {
			o := opens[s]
			k := keys[s]
			before := fake.fetches.Load()
			// A client retries a title that is still being fetched (Plex
			// gets a "not ready" at the 2 s wait); two tries cover it.
			var code int
			var body []byte
			for try := 0; try < 2; try++ {
				req, err := http.NewRequest(o.method, server.URL+o.path, strings.NewReader(o.body))
				if err != nil {
					t.Fatal(err)
				}
				for h, v := range o.header {
					req.Header.Set(h, v)
				}
				res, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				body, _ = io.ReadAll(res.Body)
				res.Body.Close()
				code = res.StatusCode
				if code == http.StatusOK {
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
			if code != http.StatusOK {
				t.Fatalf("open %s %s: %d %.300s", o.method, o.path, code, body)
			}
			deadline := time.Now().Add(5 * time.Second)
			for !stored(k) && time.Now().Before(deadline) {
				time.Sleep(50 * time.Millisecond)
			}
			if !stored(k) {
				t.Fatalf("opening %d did not store it again", k)
			}
			if fake.fetches.Load() == before {
				t.Fatalf("opening %d did not fetch it from TMDB", k)
			}
			if name := fmt.Sprintf("Film %d", k.TMDB()); !strings.Contains(string(body), name) && s != "xtream" && s != "dlna" {
				t.Errorf("response does not carry the title %q: %.300s", name, body)
			}
		})
	}
}
