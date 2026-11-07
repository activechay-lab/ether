package remotetranscode

import (
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"strings"
	"testing"
	"time"
)

func TestAuthoredRampTransport(t *testing.T) {
	w, f, s := fixture(t)
	r := recoveryTransportRequest()
	s.Info, s.Decision, s.Protocol, s.SegmentDuration = r.Info, r.Decision, r.Protocol, 4*time.Second
	s.Ramp = core.HLSRamp{Version: 1, Segments: 2, Duration: time.Second}
	if rpc(w, "start", s, true).Code != 422 || f.calls != 0 {
		t.Fatal("unnegotiated ramp accepted")
	}
	w.opts.Capabilities.AuthoredRamp = true
	if state(t, rpc(w, "start", s, true)) != "running" {
		t.Fatal("authored ramp rejected")
	}
	defer close(f.session.done)
	if f.request.Ramp != s.Ramp {
		t.Fatalf("ramp changed: %+v", f.request.Ramp)
	}
	b, _ := json.Marshal(Start{})
	if strings.Contains(string(b), "Ramp") {
		t.Fatal("legacy wire changed")
	}
	if backendRamp(core.HLSRamp{}) != (core.HLSRamp{Version: 1}) {
		t.Fatal("legacy backend inherited config")
	}
}
