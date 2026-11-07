//go:build windows

package outbound

import (
	"net"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWinsockUnreachableErrors(t *testing.T) {
	for _, errno := range []error{windows.WSAECONNREFUSED, windows.WSAENETUNREACH, windows.WSAEHOSTUNREACH} {
		if !endpointUnreachable(&net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connectex", errno)}) {
			t.Fatalf("%v not classified as unreachable", errno)
		}
	}
	if endpointUnreachable(&net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connectex", windows.WSAETIMEDOUT)}) {
		t.Fatal("timeout classified as explicit unreachability")
	}
}
