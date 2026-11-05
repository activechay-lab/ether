package remotetranscode

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestIndexedRequestAdmissionIsCompleteAndOptIn(t *testing.T) {
	_, _, start := fixture(t)
	req := core.HLSRequest{Decision: start.Decision, Info: core.MediaInfo{Keyframes: []core.KeyframePoint{{Offset: 123, Time: 0}, {Offset: 9000, Time: 4 * time.Second}}}}
	if SupportsRequest(req, Supported()) {
		t.Fatal("indexed request admitted without explicit capability")
	}
	caps := Supported()
	caps.Keyframes = true
	if !SupportsRequest(req, caps) {
		t.Fatal("complete bounded index rejected")
	}
	for _, points := range [][]core.KeyframePoint{{{Offset: -1}}, {{Time: -1}}, {{Offset: 4, Time: 4}, {Offset: 3, Time: 5}}, {{Offset: 3, Time: 5}, {Offset: 4, Time: 4}}, make([]core.KeyframePoint, MaxKeyframes+1)} {
		req.Info.Keyframes = points
		if SupportsRequest(req, caps) {
			t.Fatal("invalid/overbound index admitted")
		}
	}
	req.Info.Keyframes = make([]core.KeyframePoint, MaxKeyframes)
	for i := range req.Info.Keyframes {
		req.Info.Keyframes[i] = core.KeyframePoint{Offset: int64(i) * 1_000_000_000_000_000, Time: time.Duration(i) * 1_000_000_000_000_000}
	}
	req.Decision.Reason = string(make([]byte, 8000))
	if SupportsRequest(req, caps) {
		t.Fatal("full DTO overflow admitted despite point cap")
	}
}

func checkKeyframeRoundTrip(t *testing.T, a *Adapter, f *fakeEncoder, request core.HLSRequest, session core.HLSSession) {
	t.Helper()
	want := append([]core.KeyframePoint(nil), request.Info.Keyframes...)
	if f.request.Decision.Enhancement != request.Decision.Enhancement || f.request.Decision.Enhancement.State != "off" {
		t.Fatal("genuine inert enhancement status stripped or mutated")
	}
	if !reflect.DeepEqual(f.request.Info.Keyframes, want) {
		t.Fatal("backend lost complete indexed source metadata")
	}
	if len(want) == 0 {
		t.Fatal("fixture missing real index")
	}
	request.Info.Keyframes[0].Offset++
	if !reflect.DeepEqual(f.request.Info.Keyframes, want) {
		t.Fatal("backend index shares caller ownership")
	}
	if _, _, err := a.StartHLSOwned(context.Background(), request); err == nil {
		t.Fatal("changed index reused immutable generation")
	}
	remote := session.(*Session)
	if remote.EncoderErr() != core.ErrEncoderPending {
		t.Fatal("changed index discarded existing execution responsibility")
	}
	request.Info.Keyframes[0].Offset--
}
