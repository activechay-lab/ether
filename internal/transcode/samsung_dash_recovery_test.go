package transcode

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// Fail the selected hardware graph in a real child process; software retries
// still run the unmodified FFmpeg command and must produce decodable media.
type samsungFailedHardwareRunner struct {
	ExecRunner
	calls atomic.Int32
}

func (r *samsungFailedHardwareRunner) Start(c Command) (Process, error) {
	if strings.Contains(argValue(c.Args, "-c:v"), "vaapi") {
		r.calls.Add(1)
		c.Args = []string{"-hide_banner", "-f", "lavfi", "-i", "color", "-vf", "missing_samsung_hw_filter", "-f", "null", "-"}
	}
	return r.ExecRunner.Start(c)
}

func TestSamsungDASHRecoveryAndMain10Copy(t *testing.T) {
	t.Run("hardware startup recovery", func(t *testing.T) {
		runner := &samsungFailedHardwareRunner{}
		e := newRealEnv(t, WithoutHWDetection(), WithRunner(runner))
		e.gen(t, "source.mkv", 6, "-c:v", "libx264", "-preset", "ultrafast", "-g", "24", "-c:a", "aac")
		info, err := e.m.Probe(context.Background(), e.url("source.mkv"))
		if err != nil {
			t.Fatal(err)
		}
		e.m.hw = core.HWAccel{Name: AccelVAAPI, Encoders: []string{"h264_vaapi"}, Device: DefaultVAAPIDevice, NoGPUScale: true}
		e.m.feat.encoders["h264_vaapi"] = true
		ds := startDASH(t, e.m, core.HLSRequest{SessionID: "hw-recovery", InputURL: e.url("source.mkv"), Info: *info, Decision: core.Decision{Kind: core.DecisionTranscode, VideoCodec: "h264", AudioCodec: "aac", AudioStream: 1, AudioCopy: true}})
		for rep, want := range []string{"h264", "aac"} {
			st, _ := e.probeDASH(t, "recovery.mp4", dashGet(t, ds, rep, InitSegment), dashGet(t, ds, rep, 0))
			if st.CodecName != want {
				t.Fatalf("codec %s want %s", st.CodecName, want)
			}
		}
		if runner.calls.Load() != 1 {
			t.Fatalf("hardware attempts %d want 1", runner.calls.Load())
		}
	})
	t.Run("Main10 AC3 copy", func(t *testing.T) {
		e := newRealEnv(t, WithoutHWDetection())
		e.gen(t, "main10.mkv", 6, "-c:v", "libx265", "-pix_fmt", "yuv420p10le", "-preset", "ultrafast", "-x265-params", "pools=1:frame-threads=1:keyint=24", "-c:a", "ac3", "-ac", "6")
		info, err := e.m.Probe(context.Background(), e.url("main10.mkv"))
		if err != nil {
			t.Fatal(err)
		}
		ds := startDASH(t, e.m, core.HLSRequest{SessionID: "main10-copy", InputURL: e.url("main10.mkv"), Info: *info, Decision: core.Decision{Kind: core.DecisionRemux, VideoCodec: "hevc", AudioCodec: "ac3", VideoCopy: true, AudioCopy: true, AudioStream: 1}})
		doc := dashManifest(t, ds)
		if !strings.HasPrefix(doc.Period.Sets[0].Reps[0].Codecs, "hvc1.2.") || doc.Period.Sets[1].Reps[0].Codecs != "ac-3" {
			t.Fatalf("unexpected codecs %+v", doc)
		}
		for rep, want := range []string{"hevc", "ac3"} {
			st, _ := e.probeDASH(t, "main10.mp4", dashGet(t, ds, rep, InitSegment), dashGet(t, ds, rep, 0))
			if st.CodecName != want {
				t.Fatalf("codec %s want %s", st.CodecName, want)
			}
		}
	})
}
