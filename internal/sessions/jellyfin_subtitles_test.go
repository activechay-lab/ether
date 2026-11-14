package sessions

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestJellyfinSubtitleJSONFromEmbeddedTracks(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is needed for real embedded subtitle extraction")
	}
	ctx := context.Background()
	p := &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 1}
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	events := NewEventBus(nil, active)
	defer events.Close()
	b, err := NewBroker(BrokerDeps{SurfacePolicy: testSessionPolicy, Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
		return &core.Title{Key: k}, nil
	}, Playback: p, Transcoder: fakeBrokerTranscoder{}, Progress: &fakeProgress{}, ActiveViewer: active, CanPlay: func(*core.Viewer, bool, time.Time) bool { return true }, BitrateLimit: func(*core.Viewer) int { return 0 }, OnTimeline: func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {}, Events: events,
		ExtractSubtitle: func(ctx context.Context, _ string, index int, _ string, w io.Writer) error {
			// Use the real extractor's absolute stream mapping and WebVTT output.
			cmd := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-i", "testdata/jellyfin-subtitles/tracks.mkv", "-map", "0:"+strconv.Itoa(index), "-f", "webvtt", "pipe:1")
			cmd.Stdout = w
			return cmd.Run()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	viewer := &core.Viewer{ID: 2}
	device := core.Device{ID: "tv"}
	plan, err := b.Plan(ctx, frontend.PlanRequest{Viewer: viewer, Device: device, Protocol: "jellyfin", Item: core.MovieKey(1), Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true}}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := b.Open(ctx, frontend.OpenRequest{Viewer: viewer, Device: device, Protocol: "jellyfin", PlanID: plan.ID})
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	for _, ps := range p.sessions {
		ps.Info.Streams = []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "ffv1"},
			{Index: 1, Type: "audio", Codec: "pcm_u8"},
			{Index: 2, Type: "subtitle", Codec: "subrip", Language: "eng", Title: "English"},
			{Index: 3, Type: "subtitle", Codec: "subrip", Language: "eng", Title: "English SDH"},
		}
	}
	p.mu.Unlock()
	access := frontend.SessionAccess{Viewer: viewer, Device: device, Protocol: "jellyfin"}
	// The client selects each track, then switches back in the same session.
	for _, index := range []int{2, 3, 2} {
		path, err := b.SubtitleURL(ctx, access, session.ID, index, "vtt")
		if err != nil {
			t.Fatal(err)
		}
		// htmlVideoPlayer.fetchSubtitles replaces .vtt with .js.
		path = strings.Replace(path, ".vtt", ".js", 1)
		w := httptest.NewRecorder()
		b.ServeMedia(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != 200 || w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
			t.Fatalf("track %d: %d %s", index, w.Code, w.Body.String())
		}
		var result struct {
			TrackEvents []struct {
				Id                 string
				Text               string
				StartPositionTicks int64
				EndPositionTicks   int64
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.TrackEvents) != 2 {
			t.Fatalf("track %d events: %+v", index, result)
		}
		want := `Hello, citizen.\NSecond line.`
		if index == 3 {
			want = `[door opens]\NHello, citizen.`
		}
		event := result.TrackEvents[0]
		if event.Id != "1" || event.Text != want || event.StartPositionTicks != 11250000 || event.EndPositionTicks != 27500000 {
			t.Fatalf("track %d event: %+v", index, event)
		}
	}
}
