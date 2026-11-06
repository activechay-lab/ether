package transcode

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// Real FFmpeg proves that expediting a media downswitch still uses the shared
// timestamps and preserves audio. It does not simulate a browser or a link.
// The copy-primary case keeps ABRLadderUnderCopy explicit: UpdateConfig
// replaces the whole transcode config.
func TestIntegrationAdaptiveRapidMediaHandoff(t *testing.T) {
	testIntegrationAdaptiveRapidMediaHandoff(t, true)
}

// Additional encode-primary handoff. Copy and encode alignment are different
// behaviors; this does not replace the copy-primary case.
func TestIntegrationAdaptiveRapidEncodeHandoff(t *testing.T) {
	testIntegrationAdaptiveRapidMediaHandoff(t, false)
}

func testIntegrationAdaptiveRapidMediaHandoff(t *testing.T, copyPrimary bool) {
	e := newRealEnv(t, WithoutHWDetection(), WithBuffering(24*time.Second, 8*time.Second, 8*time.Second), withRungDwell(8*time.Second))
	e.gen(t, "adaptive.mkv", 16, "-vf", "scale=854:480", "-c:v", "libx264", "-threads", "2", "-preset", "ultrafast", "-g", "48", "-sc_threshold", "0", "-c:a", "aac", "-ac", "2")
	if !copyPrimary {
		e.requireNonnegativeSourceClock(t, "adaptive.mkv")
	}
	info, err := e.m.Probe(t.Context(), e.url("adaptive.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	info.Keyframes = e.keyframeIndex(t, "adaptive.mkv")
	e.m.UpdateConfig(config.Transcode{ABRLadder: true, ABRLadderUnderCopy: true})
	for _, transport := range []string{ContainerMPEGTS, ContainerFMP4, core.ProtocolDASH} {
		t.Run(transport, func(t *testing.T) {
			proto, container := core.ProtocolHLS, transport
			if transport == core.ProtocolDASH {
				proto, container = core.ProtocolDASH, ContainerFMP4
			}
			d := core.Decision{Kind: core.DecisionRemux, Container: container, VideoCopy: true, AudioCopy: true, AudioStream: 1}
			if !copyPrimary {
				d = core.Decision{Kind: core.DecisionTranscode, Container: container, VideoCodec: "h264", MaxHeight: 720, BitrateKbps: 8000, AudioStream: 1, AudioCodec: "aac"}
			}
			hs, err := e.m.StartHLS(t.Context(), core.HLSRequest{SessionID: "adaptive-" + transport, InputURL: e.url("adaptive.mkv"), Info: *info, Decision: d, Protocol: proto, SegmentDuration: 2 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer hs.Close()
			s := hs.(*Session)
			if s.decision.VideoCopy != copyPrimary {
				t.Fatalf("video copy = %v, want %v", s.decision.VideoCopy, copyPrimary)
			}
			if len(s.rungs) < 2 {
				t.Fatalf("no lower rendition: %+v", s.rungs)
			}
			var previousAudio float64
			var previousBody, previousAudioBody []byte
			for n := 0; n < 2; n++ {
				if proto == core.ProtocolDASH {
					previousBody = dashGet(t, s, 0, n)
					previousAudioBody = dashGet(t, s, 1, n)
					_, previousAudio = e.probeDASH(t, "previous-audio.mp4", dashGet(t, s, 1, n))
				} else {
					previousBody = fetch(t, s, n)
				}
			}
			s.mu.Lock()
			s.wantTrack = s.rungTrack(s.rungs[len(s.rungs)-1].id)
			left := s.rungDwellLocked(true, 2)
			s.mu.Unlock()
			if left != 0 {
				t.Fatalf("real delivered media still held lower rung %v", left)
			}
			started := time.Now()
			var body, init []byte
			var audioFirst float64
			var nextAudioBody []byte
			if proto == core.ProtocolDASH {
				body = dashGet(t, s, s.wantTrack, 2)
				nextAudioBody = dashGet(t, s, 1, 2)
				_, audioFirst = e.probeDASH(t, "next-audio.mp4", dashGet(t, s, 1, 2))
				if math.Abs(previousAudio-2) > 0.1 || math.Abs(audioFirst-4) > 0.1 {
					t.Fatalf("audio timeline %v -> %v", previousAudio, audioFirst)
				}
			} else {
				rec := httptest.NewRecorder()
				s.ServeRungSegment(rec, httptest.NewRequest(http.MethodGet, "/", nil), s.rungs[len(s.rungs)-1].id, 2)
				if rec.Code != http.StatusOK {
					t.Fatalf("switch %d %s", rec.Code, rec.Body.String())
				}
				body = rec.Body.Bytes()
				if container == ContainerFMP4 {
					rec = httptest.NewRecorder()
					s.ServeRungSegment(rec, httptest.NewRequest(http.MethodGet, "/", nil), s.rungs[len(s.rungs)-1].id, InitSegment)
					if rec.Code != 200 {
						t.Fatal("missing switched init")
					}
					init = rec.Body.Bytes()
				}
			}
			if !copyPrimary {
				if proto != core.ProtocolDASH {
					previousAudioBody = previousBody
					nextAudioBody = body
					if container == ContainerFMP4 {
						rec := httptest.NewRecorder()
						s.ServeSegment(rec, httptest.NewRequest(http.MethodGet, "/", nil), InitSegment)
						previousAudioBody = append(append([]byte(nil), rec.Body.Bytes()...), previousBody...)
						nextAudioBody = append(append([]byte(nil), init...), body...)
					}
				}
				_, oldEnd := e.audioPacketSpan(t, previousAudioBody, "handoff-before")
				newStart, _ := e.audioPacketSpan(t, nextAudioBody, "handoff-after")
				if math.Abs(newStart-oldEnd)*48000 > 0.5 {
					t.Fatalf("handoff audio gap/overlap %.3f samples: %.9f -> %.9f", (newStart-oldEnd)*48000, oldEnd, newStart)
				}
			}
			elapsed := time.Since(started)
			first, end, key := e.videoSpanKey(t, container, init, body, "switched-"+transport)
			if math.Abs(first-4) > 0.1 || math.Abs(end-6) > 0.1 || !key {
				t.Fatalf("switched span %.3f..%.3f key=%v", first, end, key)
			}
			ps := e.probeFile(t, e.writeTemp(t, "audio-check-"+transport+"."+segmentExt(container), append(append([]byte(nil), init...), body...)))
			if proto != core.ProtocolDASH {
				audioFirst = startTime(t, ps, "audio")
				if container == ContainerMPEGTS {
					audioFirst -= tsMuxDelay.Seconds()
				}
				if math.Abs(audioFirst-4) > 0.1 {
					t.Fatalf("audio begins %.3f, want4", audioFirst)
				}
			}
			s.mu.Lock()
			runs := s.runSeq
			s.mu.Unlock()
			if runs != 2 {
				t.Fatalf("handoff used %d runs, want2", runs)
			}
			t.Logf("real FFmpeg: lower segment ready in %v, added dwell0, video %.3f..%.3f key=%v audio %.3f runs=%d", elapsed, first, end, key, audioFirst, runs)
		})
	}
}

// Packet timestamps include codec priming. Stream start_time alone cannot
// detect a duplicated AU or a sub-frame hole during an encoder handoff.
func (e *realEnv) audioPacketSpan(t *testing.T, body []byte, name string) (float64, float64) {
	t.Helper()
	p := e.writeTemp(t, name, body)
	b, err := exec.Command(e.ffprobe, "-v", "error", "-select_streams", "a:0", "-show_packets", "-show_entries", "packet=pts_time,duration_time", "-of", "json", p).Output()
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Packets []struct {
			PTS      string `json:"pts_time"`
			Duration string `json:"duration_time"`
		}
	}
	if err = json.Unmarshal(b, &v); err != nil || len(v.Packets) == 0 {
		t.Fatalf("audio probe: %v %s", err, b)
	}
	first, err := strconv.ParseFloat(v.Packets[0].PTS, 64)
	if err != nil {
		t.Fatal(err)
	}
	last := v.Packets[len(v.Packets)-1]
	end, err := strconv.ParseFloat(last.PTS, 64)
	if err != nil {
		t.Fatal(err)
	}
	dur, err := strconv.ParseFloat(last.Duration, 64)
	if err != nil {
		t.Fatal(err)
	}
	return first, end + dur
}

// Some demuxers retain Matroska CodecDelay in a negative input origin. Those
// fixtures already have a shifted/jittering uninterrupted clock and cannot
// establish the clean-reference premise of this sample-exact restart gate.
func (e *realEnv) requireNonnegativeSourceClock(t *testing.T, name string) {
	t.Helper()
	b, err := exec.Command(e.ffprobe, "-v", "error", "-show_entries", "format=start_time", "-of", "json", e.url(name)).Output()
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Format struct {
			Start string `json:"start_time"`
		}
	}
	if err = json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	start, err := strconv.ParseFloat(v.Format.Start, 64)
	if err != nil {
		t.Fatal(err)
	}
	if start < 0 {
		t.Skipf("pre-existing source demuxer behaviour: negative Matroska CodecDelay origin %.9fs; clean-reference gate excluded", start)
	}
}
