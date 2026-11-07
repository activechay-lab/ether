//go:build windows

package outbound

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

// endpointUnreachable reports an explicit dial refusal or missing route.
// Winsock returns WSAE* codes, which the invented syscall.ECONNREFUSED,
// ENETUNREACH and EHOSTUNREACH values never match.
func endpointUnreachable(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED) || errors.Is(err, windows.WSAENETUNREACH) || errors.Is(err, windows.WSAEHOSTUNREACH) ||
		errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.EHOSTUNREACH)
}
