package homeassistant_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/braedonsaunders/ether/internal/plugins/remotetest"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/plugins/remote"
)

func TestPlaybackThroughTunnel(t *testing.T) {
	events := make(chan string, 1)
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("missing token")
		}
		events <- r.URL.Path
		_, _ = w.Write([]byte(`[]`))
	}))
	defer ha.Close()
	tunnel := remote.NewTunnelServer(remote.TunnelOptions{Auth: remotetest.Credentials{"fixture-credential": "org.ether.examples.home-assistant"}})
	defer tunnel.Close()
	server := httptest.NewServer(tunnel.Handler())
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "python3", "-c", `from plugin import home_assistant, serve; import sys; serve(home_assistant, mode='tunnel', tunnel_url=sys.argv[1], credential='fixture-credential', background=False)`, "ws"+strings.TrimPrefix(server.URL, "http"))
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	deadline := time.Now().Add(30 * time.Second)
	for !tunnel.IsConnected("org.ether.examples.home-assistant") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !tunnel.IsConnected("org.ether.examples.home-assistant") {
		t.Fatal("sidecar did not connect")
	}
	rt := tunnel.RuntimeFor("org.ether.examples.home-assistant")
	config, _ := json.Marshal(map[string]string{"url": ha.URL, "token": "fixture-token", "started": "scene.turn_on scene.movie"})
	for _, req := range []remote.Request{{Method: "POST", Path: "/ether/configure", Body: config}, {Method: "POST", Path: "/ether/event", Body: []byte(`{"events":[{"id":"1","name":"playback.started"}]}`)}} {
		res, err := rt.Call(ctx, req)
		if err != nil || res.Status != 200 {
			t.Fatal(fmt.Sprintf("response: %d %s %v", res.Status, res.Body, err))
		}
	}
	select {
	case path := <-events:
		if path != "/api/services/scene/turn_on" {
			t.Fatal(path)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("event not delivered")
	}
}
