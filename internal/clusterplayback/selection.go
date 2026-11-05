package clusterplayback

import (
	"errors"
	"fmt"

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
