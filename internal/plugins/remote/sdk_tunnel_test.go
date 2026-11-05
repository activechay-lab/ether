package remote

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/braedonsaunders/ether/sdk/go"
)

func TestSDKTunnelReconnectAndRangeCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var reject atomic.Bool
	var authentications atomic.Int32
	tun := NewTunnelServer(TunnelOptions{
		Auth: CredentialResolverFunc(func(_ context.Context, credential string) (string, error) {
			authentications.Add(1)
			if reject.Load() || credential != "secret" {
				return "", ErrUnauthorized
			}
			return "sdk.test", nil
		}),
		PingInterval: 5 * time.Millisecond,
	})
	defer tun.Close()
	server := httptest.NewServer(tun.Handler())
	defer server.Close()
	data := bytes.Repeat([]byte("0123456789abcdef"), 16384)
	plugin := sdk.New(sdk.Definition{Manifest: sdk.Manifest{ID: "sdk.test"}, Routes: sdk.Routes{
		File: func(_ *sdk.Context, _ sdk.FileRequest) (sdk.FileRangeResponse, error) {
			return sdk.FileRangeResponse{Body: data}, nil
		},
	}})
	handle, err := plugin.Start(ctx, sdk.ServeOptions{Mode: "tunnel", TunnelURL: server.URL, Credential: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	waitConnected(t, tun, "sdk.test")
	runtime := tun.RuntimeFor("sdk.test")
	defer runtime.Close()
	streamer := runtime.(Streamer)
	// File probes and seeks may close before response_end; subsequent calls must
	// still work. Include bodies far larger than WebSocket's default read limit.
	for i := 0; i < 20; i++ {
		resp, err := streamer.CallStream(ctx, Request{Path: "/ether/file/movie"})
		if err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			var b [1]byte
			if _, err := io.ReadFull(resp.Body, b[:]); err != nil {
				t.Fatal(err)
			}
		} else {
			body, err := io.ReadAll(resp.Body)
			if err != nil || !bytes.Equal(body, data) {
				t.Fatalf("range length=%d err=%v", len(body), err)
			}
		}
		resp.Body.Close()
	}
	// Hold authentication failures across at least two reconnect attempts, then
	// recover the same runtime. Failed calls are never replayed by the SDK.
	reject.Store(true)
	before := authentications.Load()
	tun.Disconnect("sdk.test")
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for authentications.Load() < before+2 || tun.IsConnected("sdk.test") {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	select {
	case <-handle.Done:
		t.Fatal("supervisor exited on disconnect")
	default:
	}
	reject.Store(false)
	waitConnected(t, tun, "sdk.test")
	resp, err := runtime.Call(ctx, Request{Path: "/manifest.json"})
	if err != nil || resp.Status != 200 {
		t.Fatalf("recovered call: %+v %v", resp, err)
	}
	// Close during retry must end the supervisor, with no later reconnect.
	reject.Store(true)
	before = authentications.Load()
	tun.Disconnect("sdk.test")
	for authentications.Load() == before {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handle.Done:
	default:
		t.Fatal("Close returned before supervisor stopped")
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Call(ctx, Request{Path: "/manifest.json"}); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("closed call: %v", err)
	}
}
