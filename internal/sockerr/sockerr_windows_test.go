//go:build windows

package sockerr

import (
	"net"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWinsockErrnos(t *testing.T) {
	for _, c := range []struct {
		check func(error) bool
		errno error
	}{
		{IsConnRefused, windows.WSAECONNREFUSED},
		{IsConnReset, windows.WSAECONNRESET},
		{IsConnAborted, windows.WSAECONNABORTED},
		{IsUnreachable, windows.WSAENETUNREACH},
		{IsUnreachable, windows.WSAEHOSTUNREACH},
		{IsAddrInUse, windows.WSAEADDRINUSE},
		{IsDescriptorExhausted, windows.WSAEMFILE},
	} {
		if !c.check(&net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connectex", c.errno)}) {
			t.Errorf("%v not matched", c.errno)
		}
	}
}
