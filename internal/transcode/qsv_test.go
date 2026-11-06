package transcode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
)

func TestQSVProbedArgumentSelection(t *testing.T) {
	for _, enc := range []string{"h264_qsv", "hevc_qsv", "av1_qsv"} {
		for _, best := range []string{qsvLA, qsvICQ, qsvVBR, "unavailable"} {
			t.Run(enc+"/"+best, func(t *testing.T) {
				fr := newFakeRunner()
				fr.runFunc = func(_ context.Context, cmd Command) error {
					if argValue(cmd.Args, "-c:v") != enc {
						return errUnhandled
					}
					mode := qsvVBR
					if hasArg(cmd.Args, "-global_quality") {
						mode = qsvICQ
					}
					if argValue(cmd.Args, "-look_ahead") == "1" {
						mode = qsvLA
					}
					if best == "unavailable" || (best == qsvICQ && mode == qsvLA) || (best == qsvVBR && mode != qsvVBR) {
						return errors.New("invalid video parameters (-15)")
					}
					return nil
				}
				hw := detector(fr, "linux").detectQSV(context.Background(), "/dev/dri/renderD129", features{encoders: map[string]bool{enc: true}})
				if best == "unavailable" {
					if len(hw.Encoders) != 0 {
						t.Fatal(hw)
					}
					return
				}
				if hw.Device != "/dev/dri/renderD129" || !contains(hw.Encoders, enc) || hw.QSVQualityModes[enc][0] != best {
					t.Fatal(hw)
				}
				for _, quality := range []bool{true, false} {
					spec := runSpec{Accel: hw, Rate: rateControl{Quality: quality, TargetKbps: 1800, MaxrateKbps: 2400, BufsizeKbps: 19200}}
					args := planEncodeArgs(spec, picturePlan{w: 1280, h: 720}, enc, strings.TrimSuffix(enc, "_qsv"), false)
					want := best
					if !quality {
						want = qsvVBR
					}
					if got := qsvMode(spec, enc); got != want {
						t.Fatalf("mode %s want %s", got, want)
					}
					if hasArg(args, "-global_quality") != (want != qsvVBR) || hasArg(args, "-look_ahead") != (want == qsvLA) {
						t.Fatal(args)
					}
					mustContain(t, args, "-maxrate 2400k", "-bufsize 19200k")
					// The actual encoder arguments must also be in the startup probe.
					if !strings.Contains(joined(qsvProbeArgs(hw.Device, enc, want)), joined(args)) {
						t.Fatal("probe drift", args)
					}
				}
			})
		}
	}
}

func TestQSVProbeCachesCodecsIndependently(t *testing.T) {
	fr := newFakeRunner()
	fr.runFunc = func(_ context.Context, cmd Command) error {
		if argValue(cmd.Args, "-c:v") == "hevc_qsv" && hasArg(cmd.Args, "-look_ahead") {
			return errors.New("unsupported")
		}
		return errUnhandled
	}
	f := features{encoders: map[string]bool{"h264_qsv": true, "hevc_qsv": true, "av1_qsv": true}}
	hw := detector(fr, "linux").detectQSV(context.Background(), DefaultVAAPIDevice, f)
	if hw.QSVQualityModes["h264_qsv"][0] != qsvLA || hw.QSVQualityModes["hevc_qsv"][0] != qsvICQ || hw.QSVQualityModes["av1_qsv"][0] != qsvLA {
		t.Fatal(hw)
	}
}

func TestSessionQSVRuntimeRetry(t *testing.T) {
	for _, tc := range []struct {
		name, first, next string
		failAll           bool
		starts            int
	}{
		{"lookahead to ICQ", qsvLA, qsvICQ, false, 2},
		{"ICQ to VBR", qsvICQ, qsvVBR, false, 2},
		{"only cached lookahead", qsvLA, qsvICQ, false, 2},
		{"live lookahead to ICQ", qsvLA, qsvICQ, false, 2},
		{"bounded retry then CPU", qsvLA, qsvICQ, true, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fr := newFakeRunner()
			fr.failStart = func(args []string) (string, bool) {
				if argValue(args, "-c:v") != "h264_qsv" {
					return "", false
				}
				mode := qsvVBR
				if hasArg(args, "-global_quality") {
					mode = qsvICQ
				}
				if hasArg(args, "-look_ahead") {
					mode = qsvLA
				}
				if tc.failAll || mode == tc.first {
					return "[h264_qsv] Error initializing the encoder: invalid video parameters (-15)", true
				}
				return "", false
			}
			m := newTestManager(t, fr, config.Transcode{HWAccel: AccelQSV})
			// Force the initial choice for both rate plans so this regression covers
			// the process retry regardless of the request's buffering policy.
			m.hw.QSVQualityModes["h264_qsv"] = []string{tc.first, tc.next}
			m.hw.QSVBitrateModes["h264_qsv"] = []string{tc.first, tc.next}
			if tc.name == "only cached lookahead" {
				m.hw.QSVQualityModes["h264_qsv"] = []string{qsvLA}
				m.hw.QSVBitrateModes["h264_qsv"] = []string{qsvLA}
			}
			req := transcodeRequest(t, "qsv-retry")
			req.LiveInput = strings.HasPrefix(tc.name, "live")
			s := startSession(t, m, req)
			if r := get(t, s, 0); r.Code != 200 {
				t.Fatalf("%d %s", r.Code, r.Body.String())
			}
			fr.mu.Lock()
			starts := append([][]string(nil), fr.starts...)
			fr.mu.Unlock()
			if len(starts) != tc.starts {
				t.Fatalf("starts %d want %d", len(starts), tc.starts)
			}
			if enc := argValue(starts[1], "-c:v"); enc != "h264_qsv" {
				t.Fatalf("retry used %s", enc)
			}
			if hasArg(starts[1], "-look_ahead") || hasArg(starts[1], "-global_quality") != (tc.next == qsvICQ) {
				t.Fatal(starts[1])
			}
			if tc.failAll && argValue(starts[2], "-c:v") != "libx264" {
				t.Fatal(starts[2])
			}
			if m.hw.QSVQualityModes["h264_qsv"][0] != tc.first {
				t.Fatal("retry mutated device capabilities")
			}
			if s.HWAccelerated() == tc.failAll {
				t.Fatal("wrong hardware status")
			}
		})
	}
}

func TestQSVParameterError(t *testing.T) {
	for _, msg := range []string{"Invalid video parameters (-15)", "Error while opening encoder", "Unsupported rate control"} {
		if !qsvParameterError(msg) {
			t.Fatal(msg)
		}
	}
	for _, msg := range []string{"", "HTTP error 403 Forbidden", "Connection timed out", "Invalid data found when processing input", "Unsupported input codec", "Error initializing an internal MFX session"} {
		if qsvParameterError(msg) {
			t.Fatal(msg)
		}
	}
}

// Ensure errors from a rejected mode are captured as probe failures, including
// stderr-only driver diagnostics, rather than silently accepting that mode.
func TestQSVProbeRejectsLookahead(t *testing.T) {
	fr := newFakeRunner()
	fr.runFunc = func(_ context.Context, cmd Command) error {
		if hasArg(cmd.Args, "-look_ahead") {
			_, _ = io.WriteString(cmd.Stderr, "invalid video parameters (-15)")
			return fmt.Errorf("exit status 1")
		}
		return errUnhandled
	}
	hw := detector(fr, "linux").detect(context.Background(), AccelQSV, "", testFeatures())
	if hw.Name != AccelQSV || hw.QSVQualityModes["h264_qsv"][0] != qsvICQ {
		t.Fatal(hw)
	}
}

func TestQSVProbeDeviceIsolation(t *testing.T) {
	fr := newFakeRunner()
	fr.runFunc = func(_ context.Context, cmd Command) error {
		if argValue(cmd.Args, "-qsv_device") == "/dev/dri/renderD129" && hasArg(cmd.Args, "-look_ahead") {
			return errors.New("unsupported rate control")
		}
		return errUnhandled
	}
	d := detector(fr, "linux")
	a := d.detect(context.Background(), AccelQSV, "/dev/dri/renderD128", testFeatures())
	b := d.detect(context.Background(), AccelQSV, "/dev/dri/renderD129", testFeatures())
	if a.QSVQualityModes["h264_qsv"][0] != qsvLA || b.QSVQualityModes["h264_qsv"][0] != qsvICQ {
		t.Fatal(a, b)
	}
}
