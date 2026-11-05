package nodewire

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/nodewire/outbound"
)

// A raw TLS peer counts *all* decrypted application bytes, including partial
// headers that would never reach an HTTP handler. Cancellation during policy
// must close the selected connection without giving HTTP a chance to write.
func TestOutboundCancellationBeforeAnyApplicationBytes(t *testing.T) {
	now := time.Now()
	root := ca(t, now)
	peer := Identity{"cluster", "peer", "peerboot"}
	local := Identity{"cluster", "local", "localboot"}
	sg, _ := NewGuard(snapshot(t, root, peer, now), func(context.Context, Identity, *x509.Certificate) error { return nil }, time.Now)
	cg, _ := NewGuard(snapshot(t, root, local, now), func(context.Context, Identity, *x509.Certificate) error { return nil }, time.Now)
	listener, e := tls.Listen("tcp", "127.0.0.1:0", sg.ServerTLS())
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	received := make(chan int, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			received <- -1
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		b, _ := io.ReadAll(conn)
		received <- len(b)
	}()
	entered, exited := make(chan struct{}), make(chan struct{})
	tc, _ := cg.ClientTLS(peer, "127.0.0.1")
	endpoint := "https://" + listener.Addr().String()
	tr, e := outbound.New(endpoint, tc, time.Second, func(ctx context.Context, cs tls.ConnectionState) error {
		defer close(exited)
		if e := cg.RecheckServer(ctx, cs, peer, "127.0.0.1"); e != nil {
			return e
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("unbounded policy")
		}
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	})
	if e != nil {
		t.Fatal(e)
	}
	defer tr.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, nil)
	req.Header.Set("X-Test-Secret", "must-never-arrive")
	result := make(chan error, 1)
	go func() {
		r, err := (&http.Client{Transport: tr}).Do(req)
		if r != nil {
			r.Body.Close()
		}
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("policy not reached")
	}
	cancel()
	if e := <-result; !errors.Is(e, context.Canceled) {
		t.Fatal("lost cancellation", e)
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("dial lost operation cancellation")
	}
	if n := <-received; n != 0 {
		t.Fatalf("rejected TLS peer received %d application bytes", n)
	}
}
