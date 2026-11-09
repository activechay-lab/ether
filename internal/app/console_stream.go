package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/cluster"
	"github.com/braedonsaunders/ether/internal/clusterbus"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// consoleStreamTopic carries console events between nodes. Every node runs
// its own in-memory hub; an event raised on one node is forwarded here so
// consoles connected to another node see it too.
const consoleStreamTopic = "console:event"

// consoleWireEvent is the clusterbus envelope for one console event.
type consoleWireEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// consoleStreamRuntime serves server-sent events to the owner console (WS2).
// Standalone servers use only the in-memory hub; clustered servers also fan
// events out over clusterbus.
type consoleStreamRuntime struct {
	hub *admin.StreamHub
	log *slog.Logger

	mu              sync.Mutex
	bus             clusterbus.Bus
	origin          string
	linked          bool
	dashboard       func(context.Context) map[string]any
	lastDash        time.Time
	dashPending     bool
	lastSessionsSig string
	sessionsPrimed  bool
}

func (a *App) buildConsoleStream(ctx context.Context, cfg config.Config) (*consoleStreamRuntime, error) {
	return &consoleStreamRuntime{
		hub:            admin.NewStreamHub(),
		log:            a.log.With("component", "console-stream"),
		origin:         clusterbus.NewOrigin(),
		sessionsPrimed: true,
	}, nil
}

func (a *App) publishDashboard() {
	if a == nil || a.streamRT == nil {
		return
	}
	a.streamRT.PublishDashboard(context.Background())
}

func (a *App) notifySessionsChanged() {
	if a == nil || a.streamRT == nil {
		return
	}
	owner := ownerPlayback{Playback: a.playback, app: a}
	if a.streamRT.PublishSessions(context.Background(), owner.localSessions()) {
		a.streamRT.PublishDashboard(context.Background())
	}
}

// Subscribers reports how many console SSE clients are currently connected.
func (r *consoleStreamRuntime) Subscribers() int {
	if r == nil || r.hub == nil {
		return 0
	}
	return r.hub.Subscribers()
}

// SetDashboard installs the dashboard payload builder (the admin handler's
// snapshot of GET /api/dashboard). It must only run when pushing a change,
// never on a timer: an idle connection costs no database work.
func (r *consoleStreamRuntime) SetDashboard(build func(context.Context) map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dashboard = build
}

// Publish raises one console event on this node and, when clustered, on
// every other node. data is marshalled to JSON; a nil data becomes null.
// Unknown types are rejected so a typo never becomes a silent hole.
func (r *consoleStreamRuntime) Publish(ctx context.Context, typ string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := r.hub.Publish(typ, raw); err != nil {
		return err
	}
	r.mu.Lock()
	bus, origin := r.bus, r.origin
	r.mu.Unlock()
	if bus == nil {
		return nil
	}
	wire, err := json.Marshal(consoleWireEvent{Type: typ, Data: raw})
	if err != nil {
		return err
	}
	if len(wire) > cluster.MaxPayload {
		// Dashboard payloads exceed the bus frame. Forward the type alone
		// and let each node rebuild its own payload; anything else that
		// large is a local-only event.
		if typ != admin.StreamDashboard {
			r.log.Warn("console event too large for cluster bus", "type", typ, "bytes", len(wire))
			return nil
		}
		wire, err = json.Marshal(consoleWireEvent{Type: typ})
		if err != nil {
			return err
		}
	}
	fwd, cancel := context.WithTimeout(ctx, clusterbus.PublishTimeout)
	defer cancel()
	return bus.Publish(fwd, consoleStreamTopic, clusterbus.Tag(origin, wire))
}

// PublishDashboard rebuilds the dashboard payload and pushes it as a
// dashboard event, at most once per second: bursts coalesce into one
// trailing push so a flurry of changes costs one rebuild.
func (r *consoleStreamRuntime) PublishDashboard(ctx context.Context) {
	r.mu.Lock()
	if r.dashboard == nil {
		r.mu.Unlock()
		return
	}
	if wait := time.Second - time.Since(r.lastDash); wait > 0 {
		if !r.dashPending {
			r.dashPending = true
			time.AfterFunc(wait, func() { r.publishDashboardNow(context.Background()) })
		}
		r.mu.Unlock()
		return
	}
	r.lastDash = time.Now()
	r.mu.Unlock()
	r.publishDashboardNow(ctx)
}

func (r *consoleStreamRuntime) publishDashboardNow(ctx context.Context) {
	r.mu.Lock()
	build := r.dashboard
	r.dashPending = false
	r.lastDash = time.Now()
	r.mu.Unlock()
	if build == nil {
		return
	}
	_ = r.Publish(ctx, admin.StreamDashboard, build(ctx))
}

// PublishSessions announces live-stream changes immediately when the active
// session signature differs from the last observed snapshot.
func (r *consoleStreamRuntime) PublishSessions(ctx context.Context, sessions []*core.PlaySession) bool {
	sig := consoleSessionsSignature(sessions)
	r.mu.Lock()
	changed := !r.sessionsPrimed || sig != r.lastSessionsSig
	r.lastSessionsSig = sig
	r.sessionsPrimed = true
	r.mu.Unlock()
	if changed {
		_ = r.Publish(ctx, admin.StreamSessions, nil)
	}
	return changed
}

func (r *consoleStreamRuntime) onClusterEvent(payload []byte) {
	origin, msg := clusterbus.Untag(payload)
	r.mu.Lock()
	mine := origin == r.origin
	r.mu.Unlock()
	if mine {
		return
	}
	var wire consoleWireEvent
	if err := json.Unmarshal(msg, &wire); err != nil {
		return
	}
	if wire.Type == admin.StreamDashboard && len(wire.Data) == 0 {
		// A dashboard too large for the bus frame: rebuild locally.
		r.mu.Lock()
		build := r.dashboard
		r.mu.Unlock()
		if build == nil {
			return
		}
		raw, err := json.Marshal(build(context.Background()))
		if err != nil {
			return
		}
		_, _ = r.hub.Publish(wire.Type, raw)
		return
	}
	_, _ = r.hub.Publish(wire.Type, wire.Data)
}

// consoleStreamHandlers mounts GET /api/stream (WS2).
func (a *App) consoleStreamHandlers(m map[string]http.Handler) {
	rt := a.streamRT
	if rt == nil {
		return
	}
	rt.mu.Lock()
	if !rt.linked {
		rt.linked = true
		if a.extendRT != nil && a.extendRT.registry != nil {
			reg := a.extendRT.registry
			// Registry notifications are synchronous and Publish never
			// blocks, so subscribing inline is safe.
			_ = reg.Subscribe(func() {
				_ = rt.Publish(context.Background(), admin.StreamExtendChanged, map[string]any{"version": reg.Version()})
			})
		}
		if bus := a.clusterNode(); bus != nil {
			rt.bus = bus
			_ = bus.Subscribe(consoleStreamTopic, rt.onClusterEvent)
		}
	}
	rt.mu.Unlock()
	m["stream"] = admin.StreamHandler(rt.hub)
}

// consoleSessionsEvery bounds how long a stream that started or ended takes
// to show on open consoles.
const consoleSessionsEvery = 2 * time.Second

// watchConsoleSessions tells open consoles when this node's set of live
// streams changes, so Overview and Activity drop an ended or idle-expired
// stream within seconds instead of at their next poll. It reads in-memory
// session snapshots only, and only while a console is connected. Other
// nodes run their own watcher; Publish forwards their events here.
func (a *App) watchConsoleSessions(ctx context.Context) {
	if a.streamRT == nil {
		return
	}
	owner := ownerPlayback{Playback: a.playback, app: a}
	a.streamRT.watchSessions(ctx, consoleSessionsEvery, owner.localSessions)
}

func (r *consoleStreamRuntime) watchSessions(ctx context.Context, every time.Duration, list func() []*core.PlaySession) {
	r.mu.Lock()
	unprimed := !r.sessionsPrimed
	r.mu.Unlock()
	if unprimed {
		sig := consoleSessionsSignature(list())
		r.mu.Lock()
		if !r.sessionsPrimed {
			r.lastSessionsSig = sig
			r.sessionsPrimed = true
		}
		r.mu.Unlock()
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if r.hub.Subscribers() == 0 {
			continue
		}
		if r.PublishSessions(ctx, list()) {
			r.PublishDashboard(ctx)
		}
	}
}

// consoleSessionsSignature identifies the visible state of live streams so
// starts, stops, decision changes, failovers and first playhead updates push
// immediately without churning on continuous byte counters.
func consoleSessionsSignature(sessions []*core.PlaySession) string {
	rows := make([]string, 0, len(sessions))
	for _, s := range sessions {
		if s == nil {
			continue
		}
		var chID, chName, chNum string
		if s.LiveChannel != nil {
			chID, chName, chNum = s.LiveChannel.ID, s.LiveChannel.Name, s.LiveChannel.Number
		}
		var rel string
		if s.Resolution != nil {
			rel = s.Resolution.Release.Name
		}
		rows = append(rows, fmt.Sprintf("%s\t%d\t%s\t%s\t%s\t%t\t%t\t%s\t%t\t%d\t%s\t%s\t%s\t%s\t%s\t%s",
			s.ID, s.Key, s.TitleName, s.Decision, s.DecisionReason,
			s.DecisionVideoCopy, s.DecisionAudioCopy, s.ViewerState,
			s.ViewOffset != nil, s.Failovers, s.Origin, s.QualityFallback,
			rel, chID, chName, chNum))
	}
	sort.Strings(rows)
	return strings.Join(rows, "\n")
}
