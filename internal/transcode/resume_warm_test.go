package transcode

import (
	"github.com/braedonsaunders/ether/internal/mediaproc"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Exercise the retained initial header against an independent, mid-file HEVC
// B-frame copy run; decodeability and monotonic packet DTS matter more than
// simply finding an output file.
func TestResumeWarmHEVC(t *testing.T) {
	e := newRealEnv(t, WithoutHWDetection(), WithBuffering(4*time.Second, 0, 16*time.Second))
	e.gen(t, "resume.mkv", 40, "-c:v", "libx265", "-preset", "ultrafast", "-pix_fmt", "yuv420p10le", "-x265-params", "pools=1:frame-threads=1:keyint=48:min-keyint=48:scenecut=0:bframes=3", "-c:a", "ac3")
	info, err := e.m.Probe(t.Context(), e.url("resume.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	info.Keyframes = e.keyframeIndex(t, "resume.mkv")
	d := core.Decision{Kind: core.DecisionRemux, Container: ContainerMatroska, VideoCopy: true, AudioCopy: false, AudioCodec: "aac", AudioStream: 1, SubtitleStream: -1}
	start := time.Now()
	hs, err := e.m.StartHLS(t.Context(), core.HLSRequest{SessionID: "resume", InputURL: e.url("resume.mkv"), Info: *info, Decision: d, ResumeHint: 24 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer hs.Close()
	s := hs.(*Session)
	s.mu.Lock()
	child, at := s.resumeWarm, s.resumeWarmAt
	s.mu.Unlock()
	if child == nil {
		t.Fatal("no speculative run")
	}
	init := fetch(t, hs, InitSegment)
	fetch(t, hs, 0)
	fetch(t, hs, 1)
	seg := fetch(t, hs, at)
	elapsed := time.Since(start)
	t.Logf("start to complete resume segment (including header and 0/1): %s", elapsed)
	e.decodesAlone(t, ContainerMatroska, init, seg, "resume-output")
	// Verify muxed packet DTS after FFmpeg's input timestamp repair.
	file := filepath.Join(e.dir, "resume-dts.mkv")
	if err := os.WriteFile(file, append(append([]byte(nil), init...), seg...), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := mediaproc.CommandContext(t.Context(), time.Minute, e.ffprobe, "-v", "error", "-select_streams", "v:0", "-show_entries", "packet=dts_time", "-of", "csv=p=0", file).Output()
	if err != nil {
		t.Fatal(err)
	}
	last, count := -1.0, 0
	for _, line := range strings.Fields(string(out)) {
		dts, err := strconv.ParseFloat(line, 64)
		if err != nil {
			continue
		} // first reordered frames may have no DTS
		if dts < last {
			t.Fatalf("DTS moved backwards: %f -> %f", last, dts)
		}
		last = dts
		count++
	}
	if count == 0 {
		t.Fatal("no packet DTS")
	}
	child.mu.Lock()
	if child.speculative {
		t.Error("resume was not promoted")
	}
	child.mu.Unlock()
	if err := hs.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-child.encoder.done:
	case <-time.After(10 * time.Second):
		t.Fatal("child outlived retirement")
	}
}
