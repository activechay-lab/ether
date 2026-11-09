//go:build scorecard && !plexparity

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/compactids"
	"github.com/braedonsaunders/ether/internal/identity"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Only external release discovery/download is replaced by the scorecard file
// adapter. Authentication, mounted router, neutral policy, resolver, cache,
// playback manager, broker and lazy stream service are actual app wiring.
func TestXtreamMountedOfflineOriginalFile(t *testing.T) {
	stub := httptest.NewServer(http.NotFoundHandler())
	defer stub.Close()
	t.Setenv("ETHER_PARITY_TMDB_URL", stub.URL)
	t.Setenv("ETHER_PARITY_PLEXTV_URL", stub.URL)
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.mkv")
	content := make([]byte, 8<<20)
	copy(content, []byte("offline-original-media-bytes"))
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	data, _ := json.Marshal([]ScorecardFile{{TMDB: 999, Path: path, SHA256: hex.EncodeToString(sum[:]), Info: core.ReleaseInfo{Source: core.SourceWEBDL, VideoCodec: "hevc", AudioCodec: "aac", Container: "mkv", Height: 1080}, Media: &core.MediaInfo{Container: "mkv", Size: int64(len(content)), Duration: time.Hour, Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "hevc", Height: 1080, Width: 1920}, {Index: 1, Type: "audio", Codec: "aac", Channels: 2}}}}})
	if err := os.WriteFile(filepath.Join(dir, "scorecard-files.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(c *config.Config) error {
		c.SetupComplete = true
		c.Frontends = map[string]config.Frontend{"plex": {Enabled: false}}
		c.Transcode.FFmpegPath = "/not-installed/ffmpeg"
		c.Server.DefaultStreamLimit = 4
		for j := range c.Quality.Profiles {
			c.Quality.Profiles[j].MinSizeMBPerMin = 0
			c.Quality.Profiles[j].MaxSizeMBPerMin = 0
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	a, err := New(ctx, Options{DataDir: dir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	local, _ := identity.NewLocal(a.db)
	id, err := local.CreateViewer(ctx, "offline-viewer", "password-1234")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.users.RefreshViewers(ctx); err != nil {
		t.Fatal(err)
	}
	title := core.Title{Key: core.MovieKey(999), Type: core.TypeMovie, IDs: core.IDs{TMDB: 999}, Title: "Offline movie", Year: 2000, Released: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), Popularity: 100, VoteCount: 10000}
	if err = a.catalog.UpsertTitle(ctx, title, true); err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.ExecContext(ctx, `UPDATE catalog_titles SET detail=1,detail_at=? WHERE key=?`, time.Now().Unix(), title.Key); err != nil {
		t.Fatal(err)
	}
	grants, _ := identity.NewGrants(a.db, a.users, time.Hour, nil)
	password, err := grants.Issue(ctx, id, nil, "xtream", core.Device{ID: "offline-login"})
	if err != nil {
		t.Fatal(err)
	}
	allocator, _ := compactids.New(a.db)
	streamID, err := allocator.Compact(ctx, "xtream", "item", fmt.Sprint(title.Key))
	if err != nil {
		t.Fatal(err)
	}
	if err = a.frontends.control.Toggle(ctx, "xtream", true); err != nil {
		t.Fatal(err)
	}
	a.frontends.control.Reconcile(ctx, a.cfg.Get())
	viewer, _ := a.users.ActiveUser(ctx, id)
	exp, _ := a.newSurfaceExperience("xtream")
	detail, e := exp.Item(ctx, frontend.ItemRequest{Viewer: viewer, Key: title.Key})
	if e != nil || detail.Title.Key != title.Key {
		t.Fatal("neutral item unavailable", e)
	}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	client := &http.Client{Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	check, err := client.Get(fmt.Sprintf("%s/xtream/player_api.php?username=%s&password=%s&action=get_vod_info&vod_id=%d", server.URL, frontend.XtreamUsername(password), password, streamID))
	if err != nil {
		t.Fatal(err)
	}
	if check != nil {
		bb, _ := io.ReadAll(check.Body)
		check.Body.Close()
		if check.StatusCode != 200 || !json.Valid(bb) {
			t.Fatal("mounted VOD info failed")
		}
	}
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/xtream/movie/%s/%s/%d.mkv", server.URL, frontend.XtreamUsername(password), password, streamID), nil)
	req.Header.Set("Range", "bytes=2-8")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 206 || string(body) != "fline-o" || resp.Header.Get("Content-Range") != "bytes 2-8/8388608" {
		t.Fatalf("mounted original playback %d %s", resp.StatusCode, body)
	}
}
