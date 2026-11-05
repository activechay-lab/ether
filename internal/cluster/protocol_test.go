package cluster

import (
	"sync/atomic"
	"testing"
)

func TestProtocolLifecycle(t *testing.T) {
	var version atomic.Uint32
	s := NewSolo(Local{ProtocolVersion: 7, Protocol: version.Load}, nil)
	if got := s.Self().ProtocolVersion; got != 0 {
		t.Fatalf("unready protocol=%d", got)
	}
	version.Store(1)
	if got := s.Self().ProtocolVersion; got != 1 {
		t.Fatalf("ready protocol=%d", got)
	}
	version.Store(0)
	if got := s.Self().ProtocolVersion; got != 0 {
		t.Fatalf("stopped protocol=%d", got)
	}
	if got := NewSolo(Local{ProtocolVersion: 7}, nil).Self().ProtocolVersion; got != 7 {
		t.Fatalf("legacy protocol=%d", got)
	}
}
