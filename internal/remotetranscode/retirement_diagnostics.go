package remotetranscode

import (
	"context"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodewire/outbound"
)

// Local observations only; these never enter a request or terminal proof.
type stopRPCObservation struct {
	stage, transport string
	status           int
}
type stopRPCObservationKey struct{}

func retirementErrorCategory(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, core.ErrEncoderPending):
		return "pending"
	case errors.Is(err, outbound.ErrEndpointUnavailable):
		return "endpoint_unavailable"
	case errors.Is(err, outbound.ErrPeerDenied):
		return "peer_denied"
	case errors.Is(err, ErrUncertain):
		return "uncertain"
	case errors.Is(err, ErrUnsupported):
		return "unsupported"
	default:
		return "other"
	}
}

func completionSignal(done <-chan struct{}) string {
	select {
	case <-done:
		return "signaled"
	default:
		return "pending"
	}
}

type stopJoinObservation struct {
	identity                     Identity
	stopped, starting, failed    bool
	encoder, input, gatewayInput string
}

// Called under Worker.mu, like result. Observe channels without waiting or
// invoking extra backend error getters. Each retained execution logs at most
// once per second while unchanged, plus its bounded join/seal transitions.
// Normal maintenance supplies the same bounded cadence.
func observeStopJoins(e *execution) (stopJoinObservation, bool) {
	now := time.Now()
	o := stopJoinObservation{identity: e.id, stopped: e.stopped, starting: e.starting, failed: e.failed, encoder: "absent", input: "absent", gatewayInput: "absent"}
	if e.retirement != nil {
		o.encoder = completionSignal(e.retirement.EncoderDone())
	}
	if e.input != nil {
		o.input = completionSignal(e.input.InputDone())
	}
	if e.gatewayInput != nil {
		o.gatewayInput = completionSignal(e.gatewayInput.InputDone())
	}
	if o == e.stopDiagnosticJoins && !e.stopDiagnosticAt.IsZero() && now.Sub(e.stopDiagnosticAt) < time.Second {
		return stopJoinObservation{}, false
	}
	e.stopDiagnosticAt, e.stopDiagnosticJoins = now, o
	return o, true
}
