package sockerr

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestPortableErrnos(t *testing.T) {
	wrap := func(errno error) error {
		return fmt.Errorf("op: %w", &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", errno)})
	}
	for _, c := range []struct {
		name  string
		check func(error) bool
		errno error
	}{
		{"refused", IsConnRefused, syscall.ECONNREFUSED},
		{"reset", IsConnReset, syscall.ECONNRESET},
		{"aborted", IsConnAborted, syscall.ECONNABORTED},
		{"netunreach", IsUnreachable, syscall.ENETUNREACH},
		{"hostunreach", IsUnreachable, syscall.EHOSTUNREACH},
		{"addrinuse", IsAddrInUse, syscall.EADDRINUSE},
		{"emfile", IsDescriptorExhausted, syscall.EMFILE},
		{"enfile", IsDescriptorExhausted, syscall.ENFILE},
	} {
		if !c.check(wrap(c.errno)) {
			t.Errorf("%s: wrapped %v not matched", c.name, c.errno)
		}
		if c.check(nil) || c.check(errors.New("other")) || c.check(wrap(syscall.ETIMEDOUT)) {
			t.Errorf("%s: matched an unrelated error", c.name)
		}
	}
}

// The real OS errors, whatever codes the platform reports for them.
func TestLiveSocketErrors(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, err = net.Listen("tcp", ln.Addr().String()); !IsAddrInUse(err) {
		t.Fatalf("second bind: %v", err)
	}
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := closed.Addr().String()
	closed.Close()
	conn, err := net.DialTimeout("tcp", address, 10*time.Second)
	if conn != nil {
		conn.Close()
	}
	if !IsConnRefused(err) {
		t.Fatalf("dial closed port: %v", err)
	}
}
