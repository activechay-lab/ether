package etherplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Hold a successfully delivered frame inside Write until the peer has cancelled
// its request. This models a descheduled writer without timing-based sleeps.
type tunnelWriteBarrier struct {
	net.Conn
	writesUntilBarrier atomic.Int32
	delivered          chan struct{}
	release            chan struct{}
}

func (c *tunnelWriteBarrier) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if c.writesUntilBarrier.Load() > 0 && c.writesUntilBarrier.Add(-1) == 0 {
		close(c.delivered)
		<-c.release
	}
	return n, err
}

func TestTunnelCancellationDuringWrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready := make(chan struct{})
	requestCancelled := make(chan struct{})
	result := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			result <- err
			return
		}
		defer ws.CloseNow()
		<-ready
		send := func(f TunnelFrame) error {
			b, _ := json.Marshal(f)
			return ws.Write(ctx, websocket.MessageText, b)
		}
		read := func() (TunnelFrame, error) {
			_, b, err := ws.Read(ctx)
			var f TunnelFrame
			if err == nil {
				err = json.Unmarshal(b, &f)
			}
			return f, err
		}
		if err := send(TunnelFrame{Type: "request", ID: "1", Path: "/ether/file/movie"}); err != nil {
			result <- err
			return
		}
		f, err := read()
		if err != nil || f.Type != "response_start" {
			result <- fmt.Errorf("start: %+v: %v", f, err)
			return
		}
		f, err = read()
		if err != nil || f.Type != "response_chunk" {
			result <- fmt.Errorf("chunk: %+v: %v", f, err)
			return
		}
		if err := send(TunnelFrame{Type: "cancel", ID: "1"}); err != nil {
			result <- err
			return
		}
		select {
		case <-requestCancelled:
		case <-ctx.Done():
			result <- ctx.Err()
			return
		}
		if err := send(TunnelFrame{Type: "request", ID: "2", Path: "/manifest.json"}); err != nil {
			result <- err
			return
		}
		for {
			f, err = read()
			if err != nil {
				result <- err
				return
			}
			if f.ID == "2" {
				if f.Type != "response" || f.Status != 200 {
					result <- fmt.Errorf("second response: %+v", f)
					return
				}
				result <- nil
				return
			}
		}
	}))
	defer srv.Close()
	barrier := &tunnelWriteBarrier{delivered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(barrier.release) }) }
	defer release()
	transport := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		barrier.Conn = c
		return barrier, err
	}}
	defer transport.CloseIdleConnections()
	ws, _, err := websocket.Dial(ctx, srv.URL, &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	barrier.writesUntilBarrier.Store(2)
	p := New(Definition{Manifest: Manifest{ID: "test"}, Routes: Routes{File: func(c *Context, _ FileRequest) (FileRangeResponse, error) {
		go func() { <-c.Done(); close(requestCancelled) }()
		return FileRangeResponse{Body: []byte("range")}, nil
	}}})
	done := make(chan struct{})
	go func() { p.runTunnel(ctx, ws, DefaultMaxChunkBytes); close(done) }()
	defer func() { cancel(); release(); <-done }()
	close(ready)
	select {
	case <-barrier.delivered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-requestCancelled:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	release()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestTunnelShutdownDuringReconnectHandshake(t *testing.T) {
	for _, closeHandle := range []bool{false, true} {
		t.Run(fmt.Sprintf("Close=%v", closeHandle), func(t *testing.T) {
			serverCtx, stopServer := context.WithTimeout(context.Background(), 5*time.Second)
			defer stopServer()
			ctx, cancel := context.WithCancel(serverCtx)
			defer cancel()
			retry := make(chan struct{})
			var attempts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempt := attempts.Add(1)
				if r.Header.Get("X-Ether-Plugin-Credential") != "secret" {
					t.Error("missing credential on handshake")
				}
				ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{TunnelProtocol}})
				if err != nil {
					return
				}
				defer ws.CloseNow()
				if _, _, err := ws.Read(serverCtx); err != nil {
					return
				}
				if attempt == 1 {
					b, _ := json.Marshal(TunnelFrame{Type: "welcome"})
					_ = ws.Write(serverCtx, websocket.MessageText, b)
					return // Force reconnect after the first successful handshake.
				}
				if attempt == 2 {
					close(retry)
				}
				_, _, _ = ws.Read(serverCtx) // Never send welcome on the retry.
			}))
			defer srv.Close()
			p := New(Definition{Manifest: Manifest{ID: "test"}})
			handle, err := p.Start(ctx, ServeOptions{Mode: "tunnel", TunnelURL: srv.URL, Credential: "secret"})
			if err != nil {
				t.Fatal(err)
			}
			defer handle.Close()
			select {
			case <-retry:
			case <-serverCtx.Done():
				t.Fatal(serverCtx.Err())
			}
			if closeHandle {
				if err := handle.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			select {
			case <-handle.Done:
			case <-serverCtx.Done():
				t.Fatal("shutdown did not interrupt handshake")
			}
			if attempts.Load() != 2 {
				t.Fatalf("attempts=%d", attempts.Load())
			}
		})
	}
}
