package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/clusterbus"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
)

func testStreamRuntime(bus clusterbus.Bus) *consoleStreamRuntime {
	return &consoleStreamRuntime{
		hub:    admin.NewStreamHub(),
		log:    slog.Default(),
		origin: clusterbus.NewOrigin(),
		bus:    bus,
	}
}

func receiveOne(t *testing.T, rt *consoleStreamRuntime, lastID uint64) admin.StreamEvent {
	t.Helper()
	ch, cancel := rt.hub.Subscribe(lastID)
	defer cancel()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("no console event arrived")
		return admin.StreamEvent{}
	}
}

func TestConsoleStreamPublishStandalone(t *testing.T) {
	rt := testStreamRuntime(nil)
	if err := rt.Publish(context.Background(), admin.StreamNotice, map[string]any{"msg": "hi"}); err != nil {
		t.Fatal(err)
	}
	ev := receiveOne(t, rt, 0)
	if ev.Type != admin.StreamNotice || !strings.Contains(string(ev.Data), "hi") {
		t.Fatalf("event %+v", ev)
	}
	if err := rt.Publish(context.Background(), "bogus.type", nil); err == nil {
		t.Fatal("expected an error for an unknown event type")
	}
}

func TestConsoleStreamClusterFanOut(t *testing.T) {
	hub := clusterbus.NewHub()
	nodeA, nodeB := hub.Node("a"), hub.Node("b")
	rtA, rtB := testStreamRuntime(nodeA), testStreamRuntime(nodeB)
	nodeA.Subscribe(consoleStreamTopic, rtA.onClusterEvent)
	nodeB.Subscribe(consoleStreamTopic, rtB.onClusterEvent)

	gotB := make(chan admin.StreamEvent, 4)
	chB, cancelB := rtB.hub.Subscribe(0)
	defer cancelB()
	go func() {
		for ev := range chB {
			gotB <- ev
		}
	}()

	// A's own hub sees exactly one copy: the bus echoes to A, but A's
	// origin tag makes it skip its own message.
	if err := rtA.Publish(context.Background(), admin.StreamNotice, map[string]any{"msg": "fan"}); err != nil {
		t.Fatal(err)
	}
	ev := receiveOne(t, rtA, 0)
	if ev.ID != 1 {
		t.Fatalf("node A hub id %d, want exactly one local copy", ev.ID)
	}
	select {
	case ev := <-gotB:
		if ev.Type != admin.StreamNotice || !strings.Contains(string(ev.Data), "fan") {
			t.Fatalf("node B event %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("node B never received the fanned-out event")
	}
}

func TestConsoleStreamDashboardThrottle(t *testing.T) {
	rt := testStreamRuntime(nil)
	var builds atomic.Int64
	rt.SetDashboard(func(context.Context) map[string]any {
		builds.Add(1)
		return map[string]any{"n": builds.Load()}
	})
	for i := 0; i < 3; i++ {
		rt.PublishDashboard(context.Background())
	}
	if got := builds.Load(); got != 1 {
		t.Fatalf("builds after burst: %d, want 1", got)
	}
	ev := receiveOne(t, rt, 0)
	if ev.Type != admin.StreamDashboard {
		t.Fatalf("event type %q, want dashboard", ev.Type)
	}
	// The burst coalesces into one trailing push, at most a second later.
	deadline := time.Now().Add(3 * time.Second)
	for builds.Load() != 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := builds.Load(); got != 2 {
		t.Fatalf("builds after trailing push: %d, want 2", got)
	}
}

func TestConsoleStreamLargeDashboardRebuildsPerNode(t *testing.T) {
	hub := clusterbus.NewHub()
	nodeA, nodeB := hub.Node("a"), hub.Node("b")
	rtA, rtB := testStreamRuntime(nodeA), testStreamRuntime(nodeB)
	nodeA.Subscribe(consoleStreamTopic, rtA.onClusterEvent)
	nodeB.Subscribe(consoleStreamTopic, rtB.onClusterEvent)
	big := strings.Repeat("x", 16*1024)
	rtA.SetDashboard(func(context.Context) map[string]any { return map[string]any{"pad": big} })
	rtB.SetDashboard(func(context.Context) map[string]any { return map[string]any{"pad": "rebuilt"} })

	rtA.PublishDashboard(context.Background())
	evA := receiveOne(t, rtA, 0)
	var bodyA map[string]any
	if err := json.Unmarshal(evA.Data, &bodyA); err != nil || bodyA["pad"] != big {
		t.Fatalf("node A dashboard not built locally: %v", evA.Data)
	}
	evB := receiveOne(t, rtB, 0)
	var bodyB map[string]any
	if err := json.Unmarshal(evB.Data, &bodyB); err != nil || bodyB["pad"] != "rebuilt" {
		t.Fatalf("node B dashboard not rebuilt locally: %v", evB.Data)
	}
}

func TestConsoleStreamOversizeNonDashboardStaysLocal(t *testing.T) {
	hub := clusterbus.NewHub()
	nodeA, nodeB := hub.Node("a"), hub.Node("b")
	rtA, rtB := testStreamRuntime(nodeA), testStreamRuntime(nodeB)
	nodeA.Subscribe(consoleStreamTopic, rtA.onClusterEvent)
	nodeB.Subscribe(consoleStreamTopic, rtB.onClusterEvent)

	if err := rtA.Publish(context.Background(), admin.StreamNotice, map[string]any{"pad": strings.Repeat("y", 16*1024)}); err != nil {
		t.Fatal(err)
	}
	ev := receiveOne(t, rtA, 0)
	if ev.Type != admin.StreamNotice {
		t.Fatalf("local event type %q", ev.Type)
	}
	ch, cancel := rtB.hub.Subscribe(0)
	defer cancel()
	select {
	case ev := <-ch:
		t.Fatalf("oversize notice fanned out as %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestConsoleStreamRegistrySubscription(t *testing.T) {
	rt := testStreamRuntime(nil)
	reg := extend.New()
	a := &App{streamRT: rt, extendRT: &extendRuntime{registry: reg}}
	m := map[string]http.Handler{}
	a.consoleStreamHandlers(m)
	if _, ok := m["stream"]; !ok {
		t.Fatal("no stream handler mounted")
	}
	if err := reg.Register(extend.Descriptor{
		Kind: extend.KindRankingStep, ID: "builtin", Owner: extend.Builtin, Label: extend.T("Resolution"),
	}); err != nil {
		t.Fatal(err)
	}
	ev := receiveOne(t, rt, 0)
	if ev.Type != admin.StreamExtendChanged {
		t.Fatalf("event type %q, want extend.changed", ev.Type)
	}
	var body map[string]any
	if err := json.Unmarshal(ev.Data, &body); err != nil || body["version"] == nil {
		t.Fatalf("extend.changed body %v", ev.Data)
	}
}

// Overview and Activity refetch the live streams when the set changes, so an
// ended or idle-expired stream leaves both pages at once.
func TestConsoleStreamAnnouncesSessionChanges(t *testing.T) {
	rt := testStreamRuntime(nil)
	var ids atomic.Value
	ids.Store([]string{"a"})
	list := func() []*core.PlaySession {
		var out []*core.PlaySession
		for _, id := range ids.Load().([]string) {
			out = append(out, &core.PlaySession{ID: id})
		}
		return out
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, unsubscribe := rt.hub.Subscribe(0)
	defer unsubscribe()
	go rt.watchSessions(ctx, 5*time.Millisecond, list)
	time.Sleep(40 * time.Millisecond)
	select {
	case ev := <-ch:
		t.Fatalf("unchanged streams announced %+v", ev)
	default:
	}
	ids.Store([]string{})
	select {
	case ev := <-ch:
		if ev.Type != admin.StreamSessions {
			t.Fatalf("event %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("an ended stream was not announced")
	}
}
