//go:build !windows

package sockerr

import "syscall"

var (
	connRefused         = []error{syscall.ECONNREFUSED}
	connReset           = []error{syscall.ECONNRESET}
	connAborted         = []error{syscall.ECONNABORTED}
	unreachable         = []error{syscall.ENETUNREACH, syscall.EHOSTUNREACH}
	addrInUse           = []error{syscall.EADDRINUSE}
	descriptorExhausted = []error{syscall.EMFILE, syscall.ENFILE}
)
