package transcode

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Fail the selected hardware graph in a real child process; software retries
// still run the unmodified FFmpeg command and must produce decodable media.
type samsungFailedHardwareRunner struct {
	ExecRunner
	calls atomic.Int32
	slow  bool
}

func (r *samsungFailedHardwareRunner) Start(c Command) (Process, error) {
	if strings.Contains(argValue(c.Args, "-c:v"), "vaapi") {
		r.calls.Add(1)
		c.Args = []string{"-hide_banner", "-f", "lavfi", "-i", "color", "-vf", "missing_samsung_hw_filter", "-f", "null", "-"}
		if r.slow {
			c.Args = []string{"-hide_banner", "-re", "-f", "lavfi", "-i", "color", "-progress", "pipe:1", "-f", "null", "-"}
		}
	}
	return r.ExecRunner.Start(c)
}

func TestSamsungDASHRecoveryAndMain10Copy(t *testing.T) {
	for _, slow := range []bool{false, true} {
		t.Run(fmt.Sprintf("hardware startup recovery slow=%v", slow), func(t *testing.T) {
			runner := &samsungFailedHardwareRunner{slow: slow}
			e := newRealEnv(t, WithoutHWDetection(), WithRunner(runner), WithManifestWait(2*time.Second))
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
	}
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
		// hvc1 promises parameter sets live in hvcC, never in-band.
		media := dashGet(t, ds, 0, 0)
		for off := 0; off+8 <= len(media); {
			n := int(binary.BigEndian.Uint32(media[off:]))
			if n < 8 || off+n > len(media) {
				t.Fatal("invalid box size")
			}
			if string(media[off+4:off+8]) == "mdat" {
				for p := off + 8; p < off+n; {
					if p+4 > off+n {
						t.Fatal("truncated NAL length")
					}
					size := int(binary.BigEndian.Uint32(media[p:]))
					p += 4
					if size < 2 || p+size > off+n {
						t.Fatal("invalid NAL size")
					}
					typ := (media[p] >> 1) & 63
					if typ >= 32 && typ <= 34 {
						t.Fatalf("hvc1 contains in-band parameter set %d", typ)
					}
					p += size
				}
			}
			off += n
		}
		for rep, want := range []string{"hevc", "ac3"} {
			st, _ := e.probeDASH(t, "main10.mp4", dashGet(t, ds, rep, InitSegment), dashGet(t, ds, rep, 0))
			if st.CodecName != want {
				t.Fatalf("codec %s want %s", st.CodecName, want)
			}
		}
	})
}
