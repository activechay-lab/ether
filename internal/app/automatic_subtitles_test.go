package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/sessions"
	"github.com/braedonsaunders/ether/internal/subtitles"
	"github.com/braedonsaunders/ether/internal/trackprefs"
)

// This is the composed App/broker/playback/provider flow over checked-in local
// media and HTTP fakes. It proves live track serving, not a physical client's
// discovery or automatic selection of a newly introduced track.
func TestAutomaticSubtitleBrokerFlow(t *testing.T) {
	// A hang guard, not a speed bar: when this is the process's first test to
	// open a database, New migrates the image every new database copies.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	body, err := os.ReadFile("../transcode/testdata/nativeprobe/h264_aac_movtext_faststart.mp4")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = cfg.Update(func(c *config.Config) error {
		c.SetupComplete = true
		c.Plex.Token = "test-owner"
		c.Frontends = map[string]config.Frontend{"plex": {Enabled: true}, "jellyfin": {Enabled: true, Settings: map[string]any{"discovery": false}}}
		c.Transcode.FFmpegPath = "/not-installed/ffmpeg"
		c.OpenSubtitles = config.OpenSubtitles{Enabled: true, APIKey: "offline-fixture", AutomaticPreferred: true}
		for i := range c.Quality.Profiles {
			c.Quality.Profiles[i].MinSizeMBPerMin = 0
			c.Quality.Profiles[i].MaxSizeMBPerMin = 0
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	media := &operabilityMedia{tmdb: 999, relID: "automatic-fixture", body: body}
	encoder := &operabilityTranscoder{max: 1}
	a, err := New(ctx, Options{DataDir: dir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), FixtureMedia: media, FixtureTranscoder: encoder, plextvHTTP: &http.Client{Transport: fakePlexTVTransport{}}, diskUsage: func() (int64, int64, error) { return 1 << 40, 1 << 39, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	// Test only: exercise the backend foundation; this does not qualify a client.
	a.automaticSubtitleClientSupported = func(frontend.SessionAccess) bool { return true }
	var searches, downloads atomic.Int32
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/subtitles":
			searches.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"attributes": map[string]any{"language": "nl", "release": "Automatic Fixture.2000.720p.WEB-DL.H264.AAC", "files": []any{map[string]any{"file_id": 286, "file_name": "fixture.srt"}}}}}})
		case "/download":
			downloads.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"link": provider.URL + "/file", "remaining": 99})
		case "/file":
			io.WriteString(w, "1\n00:00:00,000 --> 00:00:01,000\nAutomatische ondertiteling\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	a.subtitles = subtitles.New(subtitles.Options{Config: cfg.Read().OpenSubtitles, CacheDir: filepath.Join(dir, "subtitles"), BaseURL: provider.URL, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err = a.users.SetOwner(ctx, plextv.Account{ID: 42, Username: "fixture-owner"}, "test-owner"); err != nil {
		t.Fatal(err)
	}
	viewer, err := a.users.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	title := core.Title{Key: core.MovieKey(999), Type: core.TypeMovie, IDs: core.IDs{TMDB: 999}, Title: "Automatic Fixture", Year: 2000, Released: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), Popularity: 100, VoteCount: 10000}
	if err = a.catalog.UpsertTitle(ctx, title, true); err != nil {
		t.Fatal(err)
	}
	rules := trackprefs.Default()
	rules.SubtitleMode = "always"
	rules.SubtitleLanguages = []string{"nl", "de"}
	rules.AudioLanguages = []string{"en"}
	if err = a.trackprefs.PutRules(ctx, viewer.ID, rules); err != nil {
		t.Fatal(err)
	}
	b := a.frontends.broker
	device := core.Device{ID: "automatic-device"}
	caps := core.ClientCaps{ClientID: device.ID, Product: "offline", Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, SubtitleCodecs: []string{"srt", "mov_text"}}
	plan, err := b.Plan(ctx, frontend.PlanRequest{Viewer: viewer, Device: device, Protocol: "jellyfin", Item: title.Key, Caps: caps, Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: -1, SubtitleStream: -1}}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := b.Open(ctx, frontend.OpenRequest{Viewer: viewer, Device: device, Protocol: "jellyfin", PlanID: plan.ID})
	if err != nil {
		t.Fatal(err)
	}
	access := frontend.SessionAccess{Viewer: viewer, Device: device, Protocol: "jellyfin"}
	path, err := b.StartStream(ctx, access, session.ID, frontend.StreamRequest{Format: frontend.StreamDirect})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Range", "bytes=0-1023")
	response := httptest.NewRecorder()
	b.ServeMedia(response, request)
	if response.Code != http.StatusPartialContent && response.Code != http.StatusOK {
		t.Fatal("media", response.Code, response.Body.String())
	}
	if searches.Load() != 0 || downloads.Load() != 0 {
		t.Fatal("prepare/start/input consumed provider")
	}
	if err = b.Report(ctx, access, session.ID, frontend.Timeline{State: "playing", Position: time.Second, Duration: time.Minute}); err != nil {
		t.Fatal(err)
	}
	ledger := subtitles.NewAutomaticBudget(a.db)
	for {
		status, _ := ledger.Status(ctx, viewer.ID, title.Key)
		if status.Status == "attached" {
			break
		}
		if status.Status == "error" || status.Status == "cancelled" || status.Status == "no_match" {
			t.Fatalf("worker status %+v", status)
		}
		select {
		case <-ctx.Done():
			t.Fatal("worker did not attach", status, ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	live, err := b.CurrentSubtitleSession(ctx, access, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	source, ok := a.playback.Session(live.SourceID)
	if !ok {
		t.Fatal("source absent")
	}
	index := -1
	for _, st := range source.Info.Streams {
		if st.External && st.Language == "nl" {
			index = st.Index
		}
	}
	if index < 0 {
		t.Fatal("download absent from live source")
	}
	subtitleURL, err := b.SubtitleURL(ctx, access, session.ID, index, "vtt")
	if err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	b.ServeMedia(response, httptest.NewRequest(http.MethodGet, subtitleURL, nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Automatische ondertiteling") {
		t.Fatal("live cues", response.Code, response.Body.String())
	}
	if err = b.Report(ctx, access, session.ID, frontend.Timeline{State: "playing", Position: 2 * time.Second, Duration: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if searches.Load() != 1 || downloads.Load() != 1 || encoder.starts() != 0 {
		t.Fatal("counts", searches.Load(), downloads.Load(), encoder.starts())
	}
	// Extend the composed lifecycle matrix with authenticated observed selection.
	intents := sessions.NewSubtitleIntents(a.db)
	before, err := intents.Snapshot(ctx, viewer.ID, title.Key)
	if err != nil {
		t.Fatal(err)
	}
	off, invalid := -1, 9999
	report := func(a frontend.SessionAccess, index *int) error {
		return b.Report(ctx, a, session.ID, frontend.Timeline{State: "playing", Position: 3 * time.Second, Duration: time.Minute, SubtitleStreamIndex: index})
	}
	if err = report(access, nil); err != nil {
		t.Fatal(err)
	}
	initial := live.Options.SubtitleStream
	if err = report(access, &initial); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := intents.Snapshot(ctx, viewer.ID, title.Key)
	if unchanged != before {
		t.Fatal("routine echo marked manual", unchanged, before)
	}
	foreign := access
	foreign.Device.ID = "foreign-device"
	if err = report(foreign, &index); err == nil {
		t.Fatal("foreign selection accepted")
	}
	if err = report(access, &invalid); err == nil {
		t.Fatal("invalid selection accepted")
	}
	unchanged, _ = intents.Snapshot(ctx, viewer.ID, title.Key)
	if unchanged != before {
		t.Fatal("invalid selection wrote intent")
	}
	// A failed durable veto must leave the observation available to retry.
	if _, err = a.db.ExecContext(ctx, `CREATE TRIGGER fixture_reject_intent BEFORE INSERT ON sessions_subtitle_intent BEGIN SELECT RAISE(ABORT, 'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = report(access, &index); err == nil {
		t.Fatal("DB failure hidden")
	}
	if _, err = a.db.ExecContext(ctx, `DROP TRIGGER fixture_reject_intent`); err != nil {
		t.Fatal(err)
	}
	retry, _ := b.CurrentSubtitleSession(ctx, access, session.ID)
	if retry.Options.SubtitleStream != live.Options.SubtitleStream || retry.ManualSubtitle {
		t.Fatal("failed observation advanced", retry)
	}
	if err = report(access, &index); err != nil {
		t.Fatal(err)
	}
	selected, _ := intents.Snapshot(ctx, viewer.ID, title.Key)
	if !selected.Manual || selected.Revision <= before.Revision {
		t.Fatal("changed selection missing durable veto", selected)
	}
	if err = report(access, &off); err != nil {
		t.Fatal(err)
	}
	observed, _ := b.CurrentSubtitleSession(ctx, access, session.ID)
	if observed.Options.SubtitleStream != -1 || !observed.ManualSubtitle {
		t.Fatal("explicit off lost", observed)
	}
	// The existing command path is already explicit manual intent. Its echo
	// must not become a second intent write. No automatic command exists yet.
	if err = b.Command(ctx, viewer, session.ID, frontend.Command{Kind: frontend.CommandSetSubtitle, StreamIndex: index}); err != nil {
		t.Fatal(err)
	}
	commanded, _ := intents.Snapshot(ctx, viewer.ID, title.Key)
	if err = report(access, &index); err != nil {
		t.Fatal(err)
	}
	echoed, _ := intents.Snapshot(ctx, viewer.ID, title.Key)
	if echoed != commanded {
		t.Fatal("manual command echo duplicated intent", commanded, echoed)
	}
	t.Logf("manual_revision=%d source_revision=%s off=observed absent=preserved invalid=denied foreign_device=denied db_retry=preserved command_echo=no_write auto_command=NOT_IMPLEMENTED", echoed.Revision, live.SourceRevision)

	t.Logf("source=%s revision=%s stream=%d provider_searches=%d downloads=%d encoder_starts=%d cues=served selection=NOT_PROVEN", live.SourceID, live.SourceRevision, index, searches.Load(), downloads.Load(), encoder.starts())
	if err = b.Stop(ctx, access, session.ID); err != nil {
		t.Fatal(err)
	}
}
