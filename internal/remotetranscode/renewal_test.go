package remotetranscode

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

func renewRPC(w *Worker, n Renewal) *httptest.ResponseRecorder {
	b, _ := json.Marshal(message{Version: Version, Op: "renew", Identity: n.Identity, Renewal: &n})
	r := httptest.NewRequest("POST", "https://worker/private", bytes.NewReader(b))
	r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
	out := httptest.NewRecorder()
	w.ServeHTTP(out, r)
	return out
}
func receipt(_ context.Context, _ string, _ Start, n Renewal) (RenewalReceipt, error) {
	return RenewalReceipt{Renewal: n, PlacementUntil: n.Deadline, SourceUntil: n.Deadline}, nil
}
func renewal(s Start) Renewal {
	return Renewal{Identity: s.Identity, Operation: 1, PreviousDeadline: s.Expires.UTC(), Deadline: s.Expires.Add(10 * time.Second).UTC()}
}
func TestRenewalHistoryAndFences(t *testing.T) {
	w, f, s := fixture(t)
	n := renewal(s)
	if state(t, rpc(w, "start", s, true)) != "running" {
		t.Fatal("start")
	}
	if renewRPC(w, n).Code != 422 {
		t.Fatal("missing validator accepted")
	}
	initialCap := w.records[s.Identity].monotonicDeadline
	w.opts.ValidateRenewal = receipt
	if state(t, renewRPC(w, n)) != "running" {
		t.Fatal("renew")
	}
	acceptedCap := w.records[s.Identity].monotonicDeadline
	if state(t, renewRPC(w, n)) != "running" {
		t.Fatal("replay")
	}
	if w.records[s.Identity].monotonicDeadline != acceptedCap {
		t.Fatal("replay reset elapsed cap")
	}
	changed := n
	changed.Deadline = changed.Deadline.Add(time.Second)
	if renewRPC(w, changed).Code != 409 {
		t.Fatal("changed replay")
	}
	for _, mutate := range []func(*Renewal){
		func(n *Renewal) { n.Identity.Controller = "other" },
		func(n *Renewal) { n.Identity.Worker = "old-boot" },
		func(n *Renewal) { n.Identity.Session = "other" },
		func(n *Renewal) { n.Identity.Generation++ },
	} {
		bad := n
		mutate(&bad)
		if renewRPC(w, bad).Code == 200 {
			t.Fatal("fence bypass")
		}
	}
	bad := n
	bad.PreviousVersion = ^uint64(0)
	bad.Operation = 0
	if renewRPC(w, bad).Code != 400 {
		t.Fatal("overflow")
	}
	// A delayed response/status must reconcile the accepted exact operation.
	st := rpc(w, "status", s, true)
	session := &Session{id: s.Identity, done: make(chan struct{})}
	if session.observe(st.Result()) != nil || session.Lease().Version != 1 || !sameRenewal(*session.Lease().Accepted, n) {
		t.Fatal("lost response reconciliation")
	}
	copy := session.Lease()
	copy.Accepted.Operation = 99
	if session.Lease().Accepted.Operation != 1 {
		t.Fatal("mutable snapshot")
	}
	// Old timer callback cannot stop a renewed execution, even if queued already.
	e := w.records[s.Identity]
	w.expireVersion(e, 0, s.Expires, initialCap)
	if e.stopped {
		t.Fatal("old timer stopped renewal")
	}
	next := Renewal{Identity: s.Identity, Operation: 2, PreviousVersion: 1, PreviousDeadline: n.Deadline, Deadline: n.Deadline.Add(time.Second)}
	if state(t, renewRPC(w, next)) != "running" {
		t.Fatal("next")
	}
	if renewRPC(w, n).Code != 409 {
		t.Fatal("older replay accepted")
	}
	if f.calls != 1 || !e.start.Expires.Equal(s.Expires) {
		t.Fatal("renew mutated start or invoked encoder")
	}
	changedStart := s
	changedStart.Expires = next.Deadline
	if rpc(w, "start", changedStart, true).Code != 409 {
		t.Fatal("start expiry mutation")
	}
	rpc(w, "stop", s, true)
	if state(t, renewRPC(w, next)) != "pending" || !e.stopped {
		t.Fatal("stopped replay resurrected")
	}
	next.Operation = 3
	next.PreviousVersion = 2
	next.PreviousDeadline = next.Deadline
	next.Deadline = next.Deadline.Add(time.Second)
	if renewRPC(w, next).Code != 409 {
		t.Fatal("renew after stop")
	}
}

func TestRenewalAuthorityAndConcurrentRetirement(t *testing.T) {
	for _, mode := range []string{"denied", "placement", "source", "receipt", "bound", "stop", "expiry", "retired"} {
		t.Run(mode, func(t *testing.T) {
			w, f, s := fixture(t)
			rpc(w, "start", s, true)
			n := renewal(s)
			entered, release := make(chan struct{}), make(chan struct{})
			w.opts.ValidateRenewal = func(ctx context.Context, peer string, original Start, got Renewal) (RenewalReceipt, error) {
				if original.Grant != s.Grant || !original.Expires.Equal(s.Expires) {
					t.Error("wrong original authority")
				}
				r, _ := receipt(ctx, peer, original, got)
				switch mode {
				case "denied":
					return r, errors.New("denied")
				case "placement":
					r.PlacementUntil = got.Deadline.Add(-time.Nanosecond)
				case "source":
					r.SourceUntil = got.Deadline.Add(-time.Nanosecond)
				case "receipt":
					r.Renewal.Operation++
				case "stop", "expiry", "retired":
					close(entered)
					<-release
				}
				return r, nil
			}
			if mode == "bound" {
				n.Deadline = time.Now().Add(2 * time.Minute).UTC()
			}
			if mode == "stop" || mode == "expiry" || mode == "retired" {
				done := make(chan int, 1)
				go func() { done <- renewRPC(w, n).Code }()
				<-entered
				switch mode {
				case "stop":
					rpc(w, "stop", s, true)
				case "expiry":
					// Simulate a deadline crossing while validation runs, without sleeps.
					w.mu.Lock()
					w.records[s.Identity].lease.Deadline = time.Now().Add(-time.Second)
					w.mu.Unlock()
				case "retired":
					close(f.session.done)
				}
				close(release)
				if <-done != 409 {
					t.Fatal("resurrected")
				}
			} else if renewRPC(w, n).Code == 200 {
				t.Fatal("invalid authority accepted")
			}
			if w.records[s.Identity].lease.Version != 0 || f.calls != 1 {
				t.Fatal("rejected renewal changed execution")
			}
		})
	}
	// Mandatory operation authorization runs even with a valid renewal validator.
	w, _, s := fixture(t)
	w.opts.ValidateRenewal = receipt
	s.Identity.Session = "denied"
	if renewRPC(w, renewal(s)).Code != 403 {
		t.Fatal("operation authorization bypass")
	}
}
