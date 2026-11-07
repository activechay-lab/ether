// Package sockerr classifies socket errors portably. On Windows, Winsock
// reports WSAE* codes (WSAECONNREFUSED is 10061), while the syscall.E*
// constants there are invented values that never match a real socket error;
// errors.Is(err, syscall.ECONNREFUSED) is therefore always false on Windows.
// Every check here matches the POSIX errno and, on Windows, its Winsock code.
package sockerr

import "errors"

// IsConnRefused reports an explicit connection refusal (ECONNREFUSED).
func IsConnRefused(err error) bool { return matches(err, connRefused) }

// IsConnReset reports a connection reset by the peer (ECONNRESET).
func IsConnReset(err error) bool { return matches(err, connReset) }

// IsConnAborted reports a connection aborted locally (ECONNABORTED).
func IsConnAborted(err error) bool { return matches(err, connAborted) }

// IsUnreachable reports a missing network or host route (ENETUNREACH,
// EHOSTUNREACH).
func IsUnreachable(err error) bool { return matches(err, unreachable) }

// IsAddrInUse reports a bind to an address already in use (EADDRINUSE).
func IsAddrInUse(err error) bool { return matches(err, addrInUse) }

// IsDescriptorExhausted reports a process or system descriptor limit
// (EMFILE, ENFILE).
func IsDescriptorExhausted(err error) bool { return matches(err, descriptorExhausted) }

func matches(err error, targets []error) bool {
	if err == nil {
		return false
	}
	for _, target := range targets {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}
