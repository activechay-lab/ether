package sessions

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
)

// StartObservation is the evidence used by stalled-start detection.
type StartObservation interface {
	core.PlaybackPosition
	Decision() core.Decision
	Info() core.MediaInfo
}

// ObserveStart accepts local evidence directly; remote evidence must be fresh
// and belong to the requested execution. A failed read never uses cached data.
func ObserveStart(ctx context.Context, source core.HLSSession, id string) (StartObservation, bool) {
	if live, ok := source.(StartObservation); ok {
		return live, true
	}
	observer, supported := source.(interface {
		Observe(context.Context) (*remotetranscode.ObservedSession, error)
	})
	if !supported {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	sample, err := observer.Observe(ctx)
	cancel()
	if err != nil || sample == nil || sample.Identity().Session != id || sample.Identity().Generation == 0 || sample.RequestStarted().IsZero() || time.Since(sample.RequestStarted()) > 2*time.Second || time.Since(sample.RequestStarted()) < 0 {
		return nil, false
	}
	return sample, true
}
