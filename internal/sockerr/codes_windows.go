//go:build windows

package sockerr

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// The syscall.E* values stay in each list: in-process code and tests may
// construct them directly even though Winsock never returns them.
var (
	connRefused         = []error{windows.WSAECONNREFUSED, syscall.ECONNREFUSED}
	connReset           = []error{windows.WSAECONNRESET, syscall.ECONNRESET}
	connAborted         = []error{windows.WSAECONNABORTED, syscall.ECONNABORTED}
	unreachable         = []error{windows.WSAENETUNREACH, windows.WSAEHOSTUNREACH, syscall.ENETUNREACH, syscall.EHOSTUNREACH}
	addrInUse           = []error{windows.WSAEADDRINUSE, syscall.EADDRINUSE}
	descriptorExhausted = []error{windows.WSAEMFILE, syscall.EMFILE, syscall.ENFILE}
)
