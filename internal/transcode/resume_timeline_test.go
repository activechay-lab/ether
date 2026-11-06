package transcode

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
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
				req = AuthorDistributedRamp(req, cfg)
				if req.Ramp.Segments != ramp.count {
					t.Fatal("ramp not authored")
				}
				initial := startSession(t, m, req)
				for n := 0; n < initial.plan.Total(); n++ {
					fr := newFakeRunner()
					drift := cfg
					drift.FastStart = false
					drift.FastStartSegments = 8
					drift.FastStartSegment = config.Duration(3 * time.Second)
					rm := newTestManager(t, fr, drift, WithoutHWDetection())
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
					boundary := initial.plan.Start(n).Seconds()
					if n > 0 {
						seek, err := strconv.ParseFloat(argValue(first, "-ss"), 64)
						if err != nil || seek < 0 || seek > boundary || boundary-seek > 3 {
							t.Fatalf("segment %d input seek %q outside bounded preroll before %.3f", n, argValue(first, "-ss"), boundary)
						}
						// Decoder/encoder preroll may read earlier input, but the
						// first output video frame must remain on the authored grid.
						if seek < boundary {
							wantTrim := "trim=start=" + fmt.Sprintf("%.3f", boundary)
							if vf := argValue(first, "-vf") + argValue(first, "-filter_complex"); !strings.Contains(vf, wantTrim+",") && !strings.Contains(vf, wantTrim+"[") && !strings.HasSuffix(vf, wantTrim) {
								t.Fatalf("segment %d video filters %q omit authored boundary %s", n, vf, wantTrim)
							}
						}
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
