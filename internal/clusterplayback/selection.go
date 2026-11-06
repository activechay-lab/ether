package clusterplayback

import (
	"context"
	"errors"
	"fmt"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
)

var (
	ErrNoEligibleTarget  = errors.New("clusterplayback: no eligible transcode target")
	ErrTargetUnavailable = errors.New("clusterplayback: target preflight unavailable")
)

// SelectionError describes only pre-reservation observations. It contains no
// node addresses, source facts, request fields or underlying transport errors.
// Exhausted counts advisory pool reads, never retirement or release evidence.
type SelectionError struct {
	Eligible, Exhausted, Unsupported, Unavailable int
}

func (e *SelectionError) Unwrap() error {
	switch {
	case e.Unavailable > 0:
		return ErrTargetUnavailable
	case e.Unsupported > 0:
		return remotetranscode.ErrUnsupported
	case e.Eligible > 0 && e.Exhausted == e.Eligible:
		return placement.ErrCapacity
	default:
		return ErrNoEligibleTarget
	}
}

func (e *SelectionError) Error() string {
	return fmt.Sprintf("%s (eligible=%d exhausted=%d unsupported=%d unavailable=%d)", e.Unwrap(), e.Eligible, e.Exhausted, e.Unsupported, e.Unavailable)
}

// retryTargetCapacity changes an immutable placement operation only after a
// definite capacity refusal. Unknown outcomes retain the exact request for
// reconciliation. A fixed candidate snapshot and node exclusions bound retries;
// every rescore refreshes capacity, eligibility and request-specific support.
func (r *Runtime) retryTargetCapacity(ctx context.Context, q core.HLSRequest, source nodewire.Identity, exclude string, target placement.Target, claims []placement.Claim, caps remotetranscode.Capabilities, encoder string, attempt func(placement.Target, []placement.Claim, remotetranscode.Capabilities, string) error) error {
	excluded := map[string]bool{exclude: true}
	var candidates []placementCandidate
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := attempt(target, claims, caps, encoder)
		var unknown *placement.UnknownOutcome
		if !errors.Is(err, placement.ErrCapacity) || errors.As(err, &unknown) {
			return err
		}
		excluded[target.NodeID] = true
		if candidates == nil {
			var readErr error
			candidates, readErr = r.placementCandidates(ctx)
			if readErr != nil {
				return readErr
			}
		}
		var selectErr error
		target, claims, caps, encoder, selectErr = r.selectTargetCandidates(ctx, q, excluded, source, candidates)
		if selectErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(selectErr, ErrNoEligibleTarget) {
				return err
			}
			return selectErr
		}
	}
}
