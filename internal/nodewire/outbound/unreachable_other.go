//go:build !windows

package outbound

import (
	"errors"
	"syscall"
)

// endpointUnreachable reports an explicit dial refusal or missing route.
func endpointUnreachable(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.EHOSTUNREACH)
}
