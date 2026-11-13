package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Subscribe before taking the snapshot: IDs let the console discard overlap.
// Queues are bounded and a slow reader reconnects for a fresh snapshot.
func (h *Handler) handleLogsStream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		return
	}
	level, query := r.URL.Query().Get("level"), r.URL.Query().Get("q")
	minLevel := parseLevel(level)
	query = strings.ToLower(strings.TrimSpace(query))
	ch := make(chan LogEntry, 128)
	h.logs.mu.Lock()
	if h.logs.subs == nil {
		h.logs.subs = make(map[chan LogEntry]struct{})
	}
	h.logs.subs[ch] = struct{}{}
	h.logs.mu.Unlock()
	defer func() {
		h.logs.mu.Lock()
		delete(h.logs.subs, ch)
		h.logs.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	send := func(kind string, data any) bool {
		body, err := json.Marshal(data)
		if err != nil {
			return false
		}
		_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, body)
		fl.Flush()
		return err == nil
	}
	if !send("snapshot", map[string]any{"entries": h.logs.Entries(1000, level, query), "total": h.logs.Len()}) {
		return
	}
	beat := time.NewTicker(25 * time.Second)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			if e.ID == 0 {
				if !send("snapshot", map[string]any{"entries": h.logs.Entries(1000, level, query), "total": h.logs.Len()}) {
					return
				}
				continue
			}
			if (minLevel != nil && levelRank(e.Level) < *minLevel) || (query != "" && !entryMatches(e, query)) {
				continue
			}
			if !send("entry", map[string]any{"entry": e, "total": h.logs.Len()}) {
				return
			}
		case <-beat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}
