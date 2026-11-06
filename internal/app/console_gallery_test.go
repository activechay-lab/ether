//go:build integration

package app

// Development fixture: shipping App, owner authentication and Cluster handlers,
// synthetic membership/playback observations. No production providers or claims.
import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/cluster"
	"github.com/braedonsaunders/ether/internal/clusterconfig"
	"github.com/braedonsaunders/ether/internal/clusterplayback"
	"github.com/braedonsaunders/ether/internal/config"
)

type consoleGallery struct {
	*cluster.Solo
	mu       sync.Mutex
	scenario string
}

func (f *consoleGallery) Mode() string { return cluster.ModePostgres }
func (f *consoleGallery) AllNodes(context.Context) ([]cluster.Node, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	nodes := []cluster.Node{}
	for i, role := range []string{"server", "source", "transcode", "delivery"} {
		d := &cluster.Diagnostics{SampledAt: now, ReadyExisting: true, AcceptingNew: true}
		if role == "transcode" {
			capacity, used := 8, 2
			d.TranscodeCapacity = &capacity
			d.Transcodes = &used
		}
		if role == "transcode" && f.scenario == "degraded" {
			d.ReadyExisting = false
			d.AcceptingNew = false
			d.ReadinessReason = "set_target"
		}
		if role == "transcode" && f.scenario == "recovering" {
			d.Draining = true
			d.AcceptingNew = false
		}
		nodes = append(nodes, cluster.Node{ID: role, Incarnation: "gallery-boot", Name: []string{"Controller", "Media source", "GPU encoder", "Delivery edge"}[i], Version: "0.3.5", LastSeen: now, StartedAt: now.Add(-time.Hour), Live: true, Leader: i == 0, Draining: d.Draining, Caps: cluster.Caps{Roles: config.NodeRoles{config.NodeRole(role)}, Transcode: role == "transcode", HWAccel: map[bool]string{true: "nvenc"}[role == "transcode"], Diagnostics: d}})
	}
	if f.scenario == "empty" {
		return []cluster.Node{}, nil
	}
	return nodes, nil
}
func (f *consoleGallery) Handler() http.Handler      { return http.NotFoundHandler() }
func (f *consoleGallery) Tick(context.Context) error { return nil }
func (f *consoleGallery) Cleanup()                   {}
func (f *consoleGallery) Close() error               { return nil }
func (f *consoleGallery) Ready() bool                { return true }
func (f *consoleGallery) Assignments(context.Context) ([]clusterplayback.Assignment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.scenario == "empty" {
		return []clusterplayback.Assignment{}, nil
	}
	at := time.Now().Add(-37 * time.Second)
	return []clusterplayback.Assignment{{SessionID: "living-room-movie", SourceNodeID: "source", EncoderNodeID: "transcode", ControllerNodeID: "server", Generation: 3, MovedAt: &at}, {SessionID: "bedroom-episode", SourceNodeID: "source", EncoderNodeID: "transcode", ControllerNodeID: "server", Generation: 1}}, nil
}
func (f *consoleGallery) RecoveryEvents(context.Context, int) ([]clusterplayback.RecoveryEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.scenario == "empty" || f.scenario == "healthy" {
		return []clusterplayback.RecoveryEvent{}, nil
	}
	at := time.Now().Add(-42 * time.Second)
	completed := at.Add(1200 * time.Millisecond)
	zero := 0
	interruption := int64(1200)
	event := clusterplayback.RecoveryEvent{SessionID: "living-room-movie", Role: "transcode", FailedNodeID: "encoder-old", ReplacementNodeID: "transcode", Generation: 3, StartedAt: at, Status: "recovering"}
	if f.scenario == "degraded" {
		event.Status = "recovered"
		event.CompletedAt = &completed
		event.InterruptionMS = &interruption
		event.ClientStalls = &zero
	}
	return []clusterplayback.RecoveryEvent{event}, nil
}
func TestConsoleGallery(t *testing.T) {
	if os.Getenv("ETHER_CONSOLE_GALLERY") != "1" {
		t.Skip("manual development gallery")
	}
	dir := t.TempDir()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = cfg.Update(func(c *config.Config) error { c.Transcode.FFmpegPath = "/not-installed/ffmpeg"; return nil }); err != nil {
		t.Fatal(err)
	}
	a, err := New(context.Background(), Options{DataDir: dir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	f := &consoleGallery{Solo: cluster.NewSolo(cluster.Local{Identity: cluster.Identity{NodeID: "server", Name: "Controller"}}, nil), scenario: "healthy"}
	a.cluster = f
	a.deployment = &deploymentRuntime{playback: f, settings: clusterconfig.Deployment{Roles: config.NodeRoles{config.RoleServer}}}
	a.deployment.ready.Store(true)
	a.deployment.quotaOpen.Store(true)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /fixture/{scenario}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.scenario = r.PathValue("scenario")
		f.mu.Unlock()
		_ = a.cfg.Update(func(c *config.Config) error { c.SetupComplete = true; return nil })
		w.WriteHeader(204)
	})
	mux.Handle("/", a.Handler())
	listener, err := net.Listen("tcp", "127.0.0.1:18735")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: mux}
	defer server.Close()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-stop:
			_ = server.Close()
		case <-done:
		}
	}()

	fmt.Println("console gallery fixture http://127.0.0.1:18735/ether/cluster")
	if err = server.Serve(listener); err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
