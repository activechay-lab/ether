package transcode

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/splicecheck"
)

// The AAC overlap state, output sample phase and HLS mux cut are independent
// clocks. Exercise their joint invariant with real downmixing and resampling.
func TestIntegrationReencodedAudioGridRestart(t *testing.T) {
	for _, rate := range []int{48000, 44100} {
		t.Run(strconv.Itoa(rate), func(t *testing.T) {
			e := newRealEnv(t, WithoutHWDetection())
			e.gen(t, "surround.mkv", 14, "-c:v", "libx264", "-preset", "ultrafast", "-threads", "2", "-g", "48", "-c:a", "ac3", "-ac", "6", "-ar", strconv.Itoa(rate))
			e.requireNonnegativeSourceClock(t, "surround.mkv")
			info, err := e.m.Probe(t.Context(), e.url("surround.mkv"))
			if err != nil {
				t.Fatal(err)
			}
			for _, container := range []string{ContainerMPEGTS, ContainerFMP4} {
				t.Run(container, func(t *testing.T) {
					dirs := make([]string, 2)
					for generation, first := range []int{0, 1} {
						dir := filepath.Join(e.dir, container+strconv.Itoa(first))
						if err := os.Mkdir(dir, 0700); err != nil {
							t.Fatal(err)
						}
						dirs[generation] = dir
						spec := runSpec{Input: e.url("surround.mkv"), Info: *info, Decision: core.Decision{Kind: core.DecisionTranscode, Container: container, VideoCodec: "h264", AudioCodec: "aac", AudioStream: 1, AudioChannels: 2, MaxHeight: 360, BitrateKbps: 1200, SubtitleStream: -1}, Start: time.Duration(first) * 4 * time.Second, StartSeg: first, SegDur: 4 * time.Second, Dir: dir, Playlist: "index.m3u8", InitName: "init.mp4", Protocol: core.ProtocolHLS}
						if out, err := exec.CommandContext(t.Context(), e.ffmpeg, buildArgs(spec)...).CombinedOutput(); err != nil {
							t.Fatalf("encode: %v %s", err, out)
						}
					}
					segment := func(k, g int) splicecheck.Segment {
						s := splicecheck.Segment{Index: k, Generation: uint64(g + 1), Path: filepath.Join(dirs[g], segmentFile(k, segmentExt(container)))}
						if container == ContainerFMP4 {
							s.InitPath = filepath.Join(dirs[g], "init.mp4")
						}
						return s
					}
					var candidate, ref []splicecheck.Segment
					for k := 0; k < 3; k++ {
						ref = append(ref, segment(k, 0))
						g := 0
						if k >= 1 {
							g = 1
						}
						candidate = append(candidate, segment(k, g))
					}
					report, err := splicecheck.Check(t.Context(), candidate, splicecheck.Options{ReencodedAudio: true, Reference: ref, SegmentDuration: 4 * time.Second, FFmpeg: e.ffmpeg, FFprobe: e.ffprobe})
					if err != nil {
						t.Fatalf("splice: %v %+v", err, report)
					}
					t.Logf("PCM continuity: %+v", report.PCMBoundaries)
				})
			}
		})
	}
}
