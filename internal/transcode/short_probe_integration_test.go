package transcode

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/mediaproc"
)

// Container dimensions and persisted pixel format do not establish that a new
// ffmpeg demuxer has seen a picture. Unmapped PCM fills its short probe first.
func TestIntegrationMatroskaDelayedVideoAnalysis(t *testing.T) {
	e := newRealEnv(t, WithoutHWDetection())
	ctx := t.Context()
	input := filepath.Join(e.dir, "delayed.mkv")
	args := []string{"-v", "error", "-nostdin", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24", "-f", "lavfi", "-i", "anullsrc=r=192000:cl=stereo", "-f", "lavfi", "-i", "sine=sample_rate=48000", "-map", "0:v", "-map", "1:a", "-map", "2:a", "-vf", "setpts=PTS+1/TB", "-t", "12", "-c:v", "libx264", "-preset", "ultrafast", "-threads", "1", "-g", "24", "-bf", "2", "-c:a:0", "pcm_s16le", "-c:a:1", "aac", input}
	if out, err := mediaproc.CommandContext(ctx, time.Minute, e.ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %.1000s", err, out)
	}
	out, err := mediaproc.CommandContext(ctx, time.Minute, e.ffprobe, "-v", "error", "-probesize", "262144", "-analyzeduration", "100000", "-select_streams", "v:0", "-show_entries", "stream=pix_fmt", "-of", "csv=p=0", input).Output()
	if err != nil || strings.TrimSpace(string(out)) != "unknown" {
		t.Fatalf("fixture did not defeat short analysis: %v %s", err, out)
	}
	info, err := e.m.Probe(ctx, e.url("delayed.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	d := core.Decision{Kind: core.DecisionRemux, Container: ContainerMatroska, VideoCopy: true, AudioCopy: true, AudioStream: 2}
	spec := baseSpec(*info, d)
	mustContain(t, buildArgs(spec), "-probesize 5000000 -analyzeduration 2000000")
	hs, err := e.m.StartHLS(ctx, core.HLSRequest{SessionID: "delayed-video", InputURL: e.url("delayed.mkv"), Info: *info, Decision: d})
	if err != nil {
		t.Fatal(err)
	}
	defer hs.Close()
	joined := append([]byte(nil), fetch(t, hs, InitSegment)...)
	for n := 0; n < 2; n++ {
		joined = append(joined, fetch(t, hs, n)...)
	}
	file := e.writeTemp(t, "joined.mkv", joined)
	// Probe and decode the actual static header plus emitted clusters together.
	out, err = mediaproc.CommandContext(ctx, time.Minute, e.ffprobe, "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=codec_name,pix_fmt,width,height", "-of", "csv=p=0", file).Output()
	if err != nil || strings.TrimSpace(string(out)) != "h264,320,180,yuv420p" {
		t.Fatalf("static header/media contract: %v %s", err, out)
	}
	out, err = mediaproc.CommandContext(ctx, time.Minute, e.ffmpeg, "-v", "warning", "-nostdin", "-xerror", "-i", file, "-f", "null", "-").CombinedOutput()
	if err != nil || strings.Contains(string(out), "DTS") || strings.Contains(string(out), "unspecified pixel format") {
		t.Fatalf("decode: %v %.2000s", err, out)
	}
}
