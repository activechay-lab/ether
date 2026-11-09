package transcode

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type damageRunner struct {
	Runner
	log    string
	cancel context.CancelFunc
	calls  int
}

func (r *damageRunner) Run(ctx context.Context, c Command) error {
	r.calls++
	io.WriteString(c.Stderr, r.log)
	if r.cancel != nil {
		r.cancel()
	}
	return ctx.Err()
}

func TestCopyDamageRequiresCompletedPositiveEvidence(t *testing.T) {
	positive := "corrupted macroblock 0 1\nerror while decoding MB 0 1\n"
	for _, tc := range []struct {
		name, log      string
		canceled, want bool
	}{
		{"clean", "", false, false}, {"timestamp", "timestamp discontinuity: 15000000", false, false},
		{"incomplete evidence", "error while decoding MB 0 1", false, false},
		{"damage", positive, false, true}, {"canceled positive", positive, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := &damageRunner{log: tc.log}
			if tc.canceled {
				r.cancel = cancel
			}
			m := &Manager{runner: r}
			req := core.HLSRequest{InputURL: "https://example.invalid/video", Info: core.MediaInfo{Duration: 12 * time.Second, Streams: []core.StreamInfo{{Type: "video", Codec: "h264"}}}}
			life := &encoderLifetime{}
			if got, _ := m.copyDamage(ctx, req, core.Decision{VideoCopy: true}, life); got != tc.want {
				t.Fatalf("damage=%v want%v", got, tc.want)
			}
			if r.calls > 2 {
				t.Fatalf("unbounded samples: %d", r.calls)
			}
			if life.work != 0 {
				t.Fatalf("sample work leaked: %d", life.work)
			}
		})
	}
}

func TestCopyDamageClockEvidence(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{
		{"timestamp discontinuity (stream id=256): 15000000, new offset= -15000000", true},
		{"timestamp discontinuity (stream id=256): -15000000, new offset= 15000000", true},
		{"timestamp discontinuity (stream id=256): 200000, new offset= -200000", false},
		{"timestamp discontinuity (stream id=256): timeout, new offset= 0", false},
	} {
		if got := (&copyDamageEvidence{text: tc.line}).clockJump(); got != tc.want {
			t.Fatalf("%q: %v", tc.line, got)
		}
	}
	normal := runSpec{Info: core.MediaInfo{Duration: time.Minute}}
	if len(inputTimestampArgs(normal)) == 0 {
		t.Fatal("clean title lost clock")
	}
	normal.HealTimestamps = true
	if len(inputTimestampArgs(normal)) != 0 || normal.titleClock() {
		t.Fatal("healed source still preserves discontinuity")
	}
}
