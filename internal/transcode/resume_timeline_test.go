package transcode

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// Recovery must replay the same authored plan, including custom ramps and
// folded tails. Assert the actual command, not just timeline arithmetic.
func TestRecoveryRebuildsAuthoredTimeline(t *testing.T) {
	for _, container := range []string{ContainerMPEGTS, ContainerFMP4} {
		for _, ramp := range []struct {
			count    int
			duration time.Duration
		}{{2, 2 * time.Second}, {3, time.Second}} {
			cfg := fastStartConfig()
			cfg.FastStartSegments, cfg.FastStartSegment = ramp.count, config.Duration(ramp.duration)
			t.Run(fmt.Sprintf("%s/%d-%s", container, ramp.count, ramp.duration), func(t *testing.T) {
				m := newTestManager(t, newFakeRunner(), cfg, WithoutHWDetection())
				req := transcodeRequest(t, "initial")
				req.Decision.Container = container
				req.Info.Duration = 29 * time.Second
				initial := startSession(t, m, req)
				for n := 0; n < initial.plan.Total(); n++ {
					fr := newFakeRunner()
					rm := newTestManager(t, fr, cfg, WithoutHWDetection())
					req.SessionID = fmt.Sprintf("resume-%d", n)
					req.Resume = &core.HLSResume{FirstSegment: n, Generation: 2}
					resumed := startSession(t, rm, req)
					if !reflect.DeepEqual(initial.plan, resumed.plan) {
						t.Fatalf("segment %d changed authored plan", n)
					}
					fr.mu.Lock()
					first := append([]string(nil), fr.starts[0]...)
					fr.mu.Unlock()
					if got := argValue(first, "-start_number"); got != fmt.Sprint(n) {
						t.Fatalf("start_number %s, want %d", got, n)
					}
					want := fmt.Sprintf("%.3f", initial.plan.Start(n).Seconds())
					if got := argValue(first, "-ss"); n > 0 && got != want {
						t.Fatalf("segment %d seek %q, want %s", n, got, want)
					}
					a, b := httptest.NewRecorder(), httptest.NewRecorder()
					initial.ServePlaylist(a, httptest.NewRequest(http.MethodGet, "/index.m3u8", nil))
					resumed.ServePlaylist(b, httptest.NewRequest(http.MethodGet, "/index.m3u8", nil))
					if a.Body.String() != b.Body.String() {
						t.Fatalf("segment %d changed authored playlist", n)
					}
					_ = resumed.Close()
				}
			})
		}
	}
}
