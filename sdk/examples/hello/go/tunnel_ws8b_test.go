//go:build ws8b

package hello_test

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/plugins/remote"
	"github.com/braedonsaunders/ether/internal/plugins/remotetest"
	hello "github.com/braedonsaunders/ether/sdk/examples/hello/go"
	sdk "github.com/braedonsaunders/ether/sdk/go"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWS8bSDKInterop(t *testing.T) {
	for _, lang := range []string{"go", "python", "typescript"} {
		t.Run(lang, func(t *testing.T) {
			s := remote.NewTunnelServer(remote.TunnelOptions{Auth: remotetest.Credentials{"testcred": "org.ether.examples.hello"}, PingInterval: 50 * time.Millisecond})
			defer s.Close()
			srv := httptest.NewServer(s.Handler())
			defer srv.Close()
			url := "ws" + strings.TrimPrefix(srv.URL, "http")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch lang {
			case "go":
				h, e := hello.NewHelloPlugin().Start(ctx, sdk.ServeOptions{Mode: "tunnel", TunnelURL: url, Credential: "testcred"})
				if e != nil {
					t.Fatal(e)
				}
				defer h.Close()
			case "python":
				p, _ := filepath.Abs("../python/plugin.py")
				code := `import importlib.util,sys
s=importlib.util.spec_from_file_location('hp',sys.argv[1]); m=importlib.util.module_from_spec(s); s.loader.exec_module(m)
m.serve(m.hello_plugin, mode='tunnel', tunnel_url=sys.argv[2], credential='testcred', background=False)`
				c := exec.CommandContext(ctx, "python3", "-c", code, p, url)
				c.Stderr = os.Stderr
				c.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
				if e := c.Start(); e != nil {
					t.Fatal(e)
				}
				defer func() { cancel(); _ = c.Wait() }()
			case "typescript":
				p, _ := filepath.Abs("../typescript/plugin.ts")
				q, _ := filepath.Abs("../../../typescript/src/serve.ts")
				code := `const p=await import(process.argv[1]); const s=await import(process.argv[2]); await s.serve(p.helloPlugin,{mode:'tunnel',tunnelUrl:process.argv[3],credential:'testcred'});`
				c := exec.CommandContext(ctx, "node", "--input-type=module", "-e", code, p, q, url)
				c.Stderr = os.Stderr
				c.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
				if e := c.Start(); e != nil {
					t.Fatal(e)
				}
				defer func() { cancel(); _ = c.Wait() }()
			}
			deadline := time.Now().Add(30 * time.Second)
			for !s.IsConnected("org.ether.examples.hello") && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if !s.IsConnected("org.ether.examples.hello") {
				t.Fatal("not connected")
			}
			r, e := s.RuntimeFor("org.ether.examples.hello").Call(ctx, remote.Request{Method: "GET", Path: "/catalog/movie/hello.json"})
			if e != nil || r.Status != 200 || !strings.Contains(string(r.Body), "Hello from Ether") {
				t.Fatalf("response %d %s %v", r.Status, r.Body, e)
			}
			results := make(chan error, 12)
			for i := 0; i < cap(results); i++ {
				go func(i int) {
					path := fmt.Sprintf("/catalog/movie/hello/search=Guest%d.json", i)
					r, err := s.RuntimeFor("org.ether.examples.hello").Call(ctx, remote.Request{Method: "GET", Path: path})
					if err != nil || r.Status != 200 || !strings.Contains(string(r.Body), fmt.Sprintf("Guest%d", i)) {
						results <- fmt.Errorf("request %d: status %d body %s: %v", i, r.Status, r.Body, err)
						return
					}
					results <- nil
				}(i)
			}
			for i := 0; i < cap(results); i++ {
				if err := <-results; err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestWS8bGoSidecarReportsDisconnect(t *testing.T) {
	s := remote.NewTunnelServer(remote.TunnelOptions{Auth: remotetest.Credentials{"testcred": "org.ether.examples.hello"}})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- hello.NewHelloPlugin().Serve(ctx, sdk.ServeOptions{Mode: "tunnel", TunnelURL: "ws" + strings.TrimPrefix(srv.URL, "http"), Credential: "testcred"})
	}()
	deadline := time.Now().Add(30 * time.Second)
	for !s.IsConnected("org.ether.examples.hello") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !s.IsConnected("org.ether.examples.hello") {
		t.Fatal("not connected")
	}
	_ = s.Close()
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "disconnected") {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Serve did not return after disconnect")
	}
}
