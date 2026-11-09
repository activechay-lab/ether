package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Console event types pushed over GET /api/stream (WS2). The set is closed:
// WS4, WS7 and WS11 raise events through consoleStreamRuntime.Publish, which
// rejects anything outside this list so a typo never becomes a silent hole.
const (
	StreamDashboard     = "dashboard"
	StreamExtendChanged = "extend.changed"
	StreamPluginsChange = "plugins.changed"
	StreamPluginsLog    = "plugins.log"
	StreamStudioReload  = "studio.reload"
	StreamAgentApproval = "agent.approval"
	StreamNotice        = "notice"
	// StreamSessions says the set of live streams changed. It carries no
	// list: Overview and Activity both refetch GET /api/status/sessions, so
	// they always show the same streams.
	StreamSessions = "sessions.changed"
)

// KnownStreamType reports whether typ is a console event type.
func KnownStreamType(typ string) bool {
	switch typ {
	case StreamDashboard,
		StreamExtendChanged,
		StreamPluginsChange,
		StreamPluginsLog,
		StreamStudioReload,
		StreamAgentApproval,
		StreamNotice,
		StreamSessions:
		return true
	}
	return false
}

// StreamEvent is one console event. On the wire it is an SSE message with
// `event:` set to the type, `data:` to the JSON payload and `id:` to the ID.
type StreamEvent struct {
	ID   uint64
	Type string
	Data json.RawMessage
}

// streamRing keeps the last events so a reconnecting console can resume with
// Last-Event-ID instead of refetching everything.
const streamRing = 512

// streamConnCap bounds one connection's queue. Publishing never blocks: a
// consumer that falls this far behind is disconnected and resumes from the
// ring, so one slow tab cannot slow the server.
const streamConnCap = 64

// streamHeartbeat is the SSE comment cadence. Comments carry no data and
// cost no database work; they only keep proxies and the console from
// timing the connection out.
const streamHeartbeat = 25 * time.Second

// StreamHub is the in-memory fan-out behind GET /api/stream. It is safe for
// concurrent use. In a cluster every node runs its own hub; the app runtime
// forwards events between nodes over clusterbus (topic console:event).
type StreamHub struct {
	mu   sync.Mutex
	ring []StreamEvent
	next uint64
	subs map[int]chan StreamEvent
	seq  int
}

// NewStreamHub returns an empty hub.
func NewStreamHub() *StreamHub {
	return &StreamHub{subs: map[int]chan StreamEvent{}}
}

// Publish appends typ/data to the ring and delivers to every subscriber.
// Unknown types are rejected. Delivery is non-blocking: a full subscriber is
// evicted (its channel is closed) so the publisher never waits on a reader.
func (h *StreamHub) Publish(typ string, data json.RawMessage) (StreamEvent, error) {
	if !KnownStreamType(typ) {
		return StreamEvent{}, fmt.Errorf("unknown console event type %q", typ)
	}
	if len(data) == 0 {
		data = json.RawMessage("null")
	}
	h.mu.Lock()
	h.next++
	ev := StreamEvent{ID: h.next, Type: typ, Data: data}
	h.ring = append(h.ring, ev)
	if len(h.ring) > streamRing {
		h.ring = append([]StreamEvent(nil), h.ring[len(h.ring)-streamRing:]...)
	}
	for id, ch := range h.subs {
		select {
		case ch <- ev:
		default:
			delete(h.subs, id)
			close(ch)
		}
	}
	h.mu.Unlock()
	return ev, nil
}

// Subscribers is the number of connected consoles.
func (h *StreamHub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// Subscribe returns every event after lastID (0 delivers nothing old), then
// live events until cancel runs. The channel is closed on cancel and when
// the consumer falls behind and is evicted.
func (h *StreamHub) Subscribe(lastID uint64) (<-chan StreamEvent, func()) {
	h.mu.Lock()
	ch := make(chan StreamEvent, streamConnCap)
	var missed []StreamEvent
	for _, ev := range h.ring {
		if ev.ID > lastID {
			missed = append(missed, ev)
		}
	}
	id := h.seq
	h.seq++
	h.subs[id] = ch
	h.mu.Unlock()
	cancel := func() {
		h.mu.Lock()
		if _, ok := h.subs[id]; ok {
			delete(h.subs, id)
			close(ch)
		}
		h.mu.Unlock()
	}
	if len(missed) > streamConnCap {
		missed = missed[len(missed)-streamConnCap:]
	}
	for _, ev := range missed {
		ch <- ev
	}
	return ch, cancel
}

// StreamHandler serves GET /api/stream as server-sent events. It sits behind
// the Extensions mount, so the LAN, token and CSRF boundaries already apply.
// The console authenticates with the X-Ether-Admin-Token header, which
// EventSource cannot send, so the console connects with fetch() streaming;
// the token stays out of the query string.
func StreamHandler(hub *StreamHub) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
			return
		}
		fl, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "streaming_unsupported", "this server cannot stream events")
			return
		}
		var lastID uint64
		if raw := strings.TrimSpace(r.Header.Get("Last-Event-ID")); raw != "" {
			lastID, _ = strconv.ParseUint(raw, 10, 64)
		} else if raw := strings.TrimSpace(r.URL.Query().Get("lastEventId")); raw != "" {
			lastID, _ = strconv.ParseUint(raw, 10, 64)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "retry: 10000\n\n")
		fl.Flush()

		serveStreamEvents(w, r, hub, lastID, streamHeartbeat, fl)
	})
}

// serveStreamEvents runs the event loop for one connection: replayed then
// live events until the client goes away, the consumer falls behind, or the
// heartbeat fires. Heartbeat is a parameter so tests can use a short
// interval; production passes streamHeartbeat.
func serveStreamEvents(w http.ResponseWriter, r *http.Request, hub *StreamHub, lastID uint64, heartbeat time.Duration, fl http.Flusher) {
	ch, cancel := hub.Subscribe(lastID)
	defer cancel()
	beat := time.NewTicker(heartbeat)
	defer beat.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-beat.C:
			_, _ = fmt.Fprintf(w, ": heartbeat %d\n\n", time.Now().Unix())
			fl.Flush()
		case ev, ok := <-ch:
			if !ok {
				// Evicted as a slow consumer: the console reconnects
				// with its last ID and replays from the ring.
				return
			}
			writeStreamEvent(w, ev)
			fl.Flush()
		}
	}
}

func writeStreamEvent(w http.ResponseWriter, ev StreamEvent) {
	_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\n", ev.ID, ev.Type)
	data := string(ev.Data)
	if data == "" {
		data = "null"
	}
	for _, line := range strings.Split(data, "\n") {
		_, _ = fmt.Fprintf(w, "data: %s\n", line)
	}
	_, _ = fmt.Fprint(w, "\n")
}

// dashboardBody builds the GET /api/dashboard body without serving HTTP, so
// the console stream can push the same payload the dashboard route serves.
func (h *Handler) dashboardBody(r *http.Request) map[string]any {
	providers := h.statusProviders()
	names := make([]string, 0, len(providers))
	for n := range providers {
		names = append(names, n)
	}
	sort.Strings(names)
	sections := map[string]any{}
	for _, n := range names {
		sections[n] = h.safeStatus(r, providers[n])
	}
	cfg := h.deps.Config.Get()
	steps, complete := h.setupStatus(r)
	plexEnabled := cfg.Frontends == nil || cfg.Frontends["plex"].Enabled
	if !plexEnabled {
		delete(sections, "remote")
		kept := names[:0]
		for _, n := range names {
			if n != "remote" {
				kept = append(kept, n)
			}
		}
		names = kept
	}
	return map[string]any{
		"sections": sections, "names": names,
		"setupComplete": complete, "setupSteps": steps, "version": h.version,
		"plexEnabled": plexEnabled,
	}
}

// SnapshotDashboard returns the dashboard payload for contexts without an
// HTTP request, such as the console stream's push path. It performs the same
// provider calls as GET /api/dashboard, so it must only run when pushing a
// change, never on a timer: an idle stream connection costs no database work.
func (h *Handler) SnapshotDashboard(ctx context.Context) map[string]any {
	if ctx == nil {
		ctx = context.Background()
	}
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/api/dashboard", nil)
	return h.dashboardBody(r)
}
