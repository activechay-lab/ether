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

// A program change is published in the health snapshot as soon as the tap
// parses it at the live edge.
func TestInputHealthPublishesFormatGeneration(t *testing.T) {
	g := &tsGen{cc: map[int]byte{}, dts: 900000, gop: 12}
	var before, after []byte
	for range 24 {
		before = append(before, g.next()...)
	}
	g.ac3 = true
	for range 24 {
		after = append(after, g.next()...)
	}
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp2t")
		w.Write(before)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Write(after)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer up.Close()
	s := testService(t, Options{HTTPClient: up.Client()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ss := &session{ctx: ctx, cancel: cancel, info: SessionInfo{ID: "fmt", Channel: Channel{ID: "fmt", Kind: "iptv"}}}
	if err := s.grantInput(ss, StreamSource{ID: "a", URL: up.URL + "/live.ts"}, http.Header{}); err != nil {
		t.Fatal(err)
	}
	defer s.revokeInput(ss)
	input, _ := s.relayInput(ss)
	res, err := http.Get(input)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	go io.Copy(io.Discard, res.Body)
	wait := func(want uint64) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if h, ok := s.inputHealth(ss); ok && h.FormatGeneration == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		h, _ := s.inputHealth(ss)
		t.Fatalf("format generation %d, want %d", h.FormatGeneration, want)
	}
	wait(1)
	close(release)
	wait(2)
}
