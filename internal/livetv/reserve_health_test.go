package livetv

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// inputHealth is read from frame clocks and under callers' own locks, so it
// must answer at once even while every lock the input's I/O uses is held:
// here the tap's, the relay's and the grant's, with the tap mid-reconnect
// to a provider that answers and then sends nothing.
func TestInputHealthNeverWaitsOnInputLocks(t *testing.T) {
	g := &tsGen{cc: map[int]byte{}, dts: 900000, gop: 12}
	var first []byte
	for range 90 {
		first = append(first, g.next()...)
	}
	var opens atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp2t")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		if opens.Add(1) == 1 {
			w.Write(first)
			w.(http.Flusher).Flush()
		}
		<-r.Context().Done() // stalled: connection open, no bytes
	}))
	defer up.Close()
	s := testService(t, Options{HTTPClient: up.Client(), Stability: func() string { return stabilityBalanced }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ss := &session{ctx: ctx, cancel: cancel, info: SessionInfo{ID: "steady-lock", Channel: Channel{ID: "lock", Kind: "iptv"}}}
	if err := s.grantSteadyInput(ss, StreamSource{ID: "a", URL: up.URL + "/live.ts"}, http.Header{}); err != nil {
		t.Fatal(err)
	}
	defer s.revokeInput(ss)
	stalled := make(chan struct{}, 1)
	defer s.watchInput(ss, func(ev UpstreamEvent) {
		if ev.Kind == InputStalled {
			select {
			case stalled <- struct{}{}:
			default:
			}
		}
	})()
	input, _ := s.relayInput(ss)
	res, err := http.Get(input)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	go io.Copy(io.Discard, res.Body)
	select {
	case <-stalled:
	case <-time.After(10 * time.Second):
		t.Fatal("no stall reported")
	}
	// The tap is reconnecting; take every lock its I/O and bookkeeping use.
	grant := s.healthGrant(ss)
	grant.mu.Lock()
	tap := grant.tap
	grant.mu.Unlock()
	s.inputRelay.mu.Lock()
	tap.mu.Lock()
	grant.mu.Lock()
	done := make(chan UpstreamHealth, 1)
	go func() {
		h, _ := s.inputHealth(ss)
		done <- h
	}()
	var h UpstreamHealth
	select {
	case h = <-done:
	case <-time.After(100 * time.Millisecond):
		grant.mu.Unlock()
		tap.mu.Unlock()
		s.inputRelay.mu.Unlock()
		t.Fatal("inputHealth waited on an input lock")
	}
	grant.mu.Unlock()
	tap.mu.Unlock()
	s.inputRelay.mu.Unlock()
	if h.Kind != "ts" || !h.Stalled || h.Connected || h.DataAge < tapStallAfter {
		t.Fatalf("health during the reconnect: %+v", h)
	}
}
