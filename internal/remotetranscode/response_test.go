package remotetranscode

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type blockedWriter struct {
	header           http.Header
	entered, release chan struct{}
	once             sync.Once
}

func (b *blockedWriter) Header() http.Header         { return b.header }
func (b *blockedWriter) block()                      { b.once.Do(func() { close(b.entered) }); <-b.release }
func (b *blockedWriter) WriteHeader(int)             { b.block() }
func (b *blockedWriter) Write(p []byte) (int, error) { b.block(); return len(p), nil }

// Network backpressure must never serialize authority enforcement behind output.
func TestBlockedResponseAllowsExpiryStopAndClose(t *testing.T) {
	for _, mode := range []string{"start", "start-failed", "renew", "renew-concurrent-duplicate", "renew-conflict", "status", "stop"} {
		t.Run(mode, func(t *testing.T) {
			w, _, s := fixture(t)
			n := renewal(s)
			w.opts.ValidateRenewal = receipt
			if mode != "start" && mode != "start-failed" {
				rpc(w, "start", s, true)
			}
			if mode == "start-failed" {
				w.opts.Resolve = func(context.Context, string, Start) (Source, error) { return Source{}, errors.New("source denied") }
			}
			if mode == "renew-concurrent-duplicate" {
				first := true
				w.opts.ValidateRenewal = func(ctx context.Context, peer string, original Start, n Renewal) (RenewalReceipt, error) {
					if first {
						first = false
						renewRPC(w, n)
					} // Other validation commits before this returns.
					return receipt(ctx, peer, original, n)
				}
			}
			if mode == "renew-conflict" {
				w.opts.ValidateRenewal = func(ctx context.Context, peer string, original Start, n Renewal) (RenewalReceipt, error) {
					rpc(w, "stop", s, true)
					return receipt(ctx, peer, original, n)
				}
			}
			out := &blockedWriter{header: make(http.Header), entered: make(chan struct{}), release: make(chan struct{})}
			returned := make(chan struct{})

			m := message{Version: Version, Identity: s.Identity, Op: mode}
			switch mode {
			case "start", "start-failed":
				m.Op = "start"
				m.Start = &s
			case "status", "stop":
			default:
				m.Op = "renew"
				m.Renewal = &n
			}
			body, _ := json.Marshal(m)
			r := httptest.NewRequest("POST", "https://worker/private", bytes.NewReader(body))
			r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
			go func() { defer close(returned); w.ServeHTTP(out, r) }()
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(out.release) }) }
			defer func() { release(); <-returned }()

			select {
			case <-out.entered:
			case <-time.After(time.Second):
				t.Fatal("response did not reach writer")
			}
			enforced := make(chan bool, 1)
			go func() {
				w.mu.Lock()
				e := w.records[s.Identity]
				e.monotonicDeadline = time.Now().Add(-time.Second)
				version, deadline, cap := e.lease.Version, e.lease.Deadline, e.monotonicDeadline
				w.mu.Unlock()
				w.expireVersion(e, version, deadline, cap)
				rpc(w, "stop", s, true)
				w.Close()
				w.mu.Lock()
				stopped := e.stopped
				w.mu.Unlock()
				enforced <- stopped
			}()
			select {
			case stopped := <-enforced:
				if !stopped {
					t.Fatal("expiry did not stop execution")
				}
			case <-time.After(time.Second):
				// Release output before failing so the old broken implementation also cleans up.
				release()
				<-returned
				<-enforced
				t.Fatal("blocked response prevented expiry/stop/close")
			}
		})
	}
}

func TestEarlyExpiryRearmsWithinMonotonicCap(t *testing.T) {
	w, _, s := fixture(t)
	rpc(w, "start", s, true)
	w.mu.Lock()
	e := w.records[s.Identity]
	previous := e.timer
	cap := e.monotonicDeadline
	version, deadline := e.lease.Version, e.lease.Deadline
	w.mu.Unlock()
	w.expireVersion(e, version, deadline, cap) // Deliberately early, no sleeping.
	w.mu.Lock()
	if e.stopped || e.timer == previous || e.monotonicDeadline != cap {
		w.mu.Unlock()
		t.Fatal("early callback lost timer or reset cap")
	}
	// Model a backward wall-clock jump: wall authority is future, elapsed cap is past.
	e.monotonicDeadline = time.Now().Add(-time.Second)
	cap = e.monotonicDeadline
	w.mu.Unlock()
	w.expireVersion(e, version, deadline, cap)
	w.mu.Lock()
	stopped := e.stopped
	w.mu.Unlock()
	if !stopped {
		t.Fatal("future wall deadline overrode expired elapsed bound")
	}
}
