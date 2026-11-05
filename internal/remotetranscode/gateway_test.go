package remotetranscode

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
)

type gatewayFixture struct {
	url    string
	lease  core.EncoderInputLease
	err    error
	id     Identity
	handle string
}

func (g *gatewayFixture) GatewayInput(id Identity, handle string) (string, core.EncoderInputLease, error) {
	g.id = id
	g.handle = handle
	return g.url, g.lease, g.err
}
func TestGatewayValidationRetainsBothErrorLeases(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		w, encoder, s := fixture(t)
		local := &fakeLease{done: make(chan struct{})}
		ordinary := &fakeLease{done: make(chan struct{})}
		gateway := &gatewayFixture{url: "http://127.0.0.1:1234/input/opaque", lease: local, err: errors.New("uncertain input")}
		w.opts.Resolve = func(context.Context, string, Start) (Source, error) {
			source := Source{Gateway: gateway}
			if mixed {
				source.Lease = ordinary
				source.URL = "https://10.0.0.2/input"
			}
			return source, nil
		}
		out := rpc(w, "start", s, true)
		if out.Code != 409 || encoder.calls != 0 || gateway.id != s.Identity || gateway.handle != s.Grant {
			t.Fatal("invalid gateway invoked encoder")
		}
		if local.InputErr() != nil {
			t.Fatal("error lost local lease")
		}
		if mixed && ordinary.InputErr() != nil {
			t.Fatal("mixed result lost ordinary lease")
		}
	}
	for _, raw := range []string{"http://localhost:123/input", "http://10.0.0.2:123/input", "http://127.0.0.1/input", "http://user@127.0.0.1:123/input", "http://127.0.0.1:123/input?", "http://127.0.0.1:123/input#token", "http://127.0.0.1:123/in%70ut"} {
		if gatewayURL(raw) {
			t.Fatal("unsafe gateway URL accepted")
		}
	}
	if !gatewayURL("http://127.0.0.1:123/input/token") {
		t.Fatal("literal loopback rejected")
	}
}
