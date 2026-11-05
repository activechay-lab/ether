package remotetranscode

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func terminalState(t *testing.T, w *Worker, id Identity, stop bool, want TerminalState) TerminalSnapshot {
	t.Helper()
	snapshot, err := w.Terminal(context.Background(), id, stop)
	if err != nil || snapshot.Identity != id || snapshot.State != want {
		t.Fatalf("exact terminal snapshot=%+v err=%v want=%s", snapshot, err, want.String())
	}
	return snapshot
}

func TestTerminalAbsentStopFencesDelayedStartAndCapacity(t *testing.T) {
	w, f, start := fixture(t)
	if _, err := w.Terminal(context.Background(), start.Identity, false); !errors.Is(err, ErrUncertain) {
		t.Fatal("status-only absence supplied retirement", err)
	}
	if len(w.records) != 0 || len(w.highest) != 0 {
		t.Fatal("absent status mutated admission state")
	}
	snapshot := terminalState(t, w, start.Identity, true, TerminalRetired)
	if !snapshot.Stopped || w.highest[start.Identity.Controller+"/"+start.Identity.Session] != start.Identity.Generation {
		t.Fatal("stop proof published without tombstone floor")
	}
	if rpc(w, "start", start, true).Code != 409 || f.calls != 0 {
		t.Fatal("delayed stopped generation started")
	}
	terminalState(t, w, start.Identity, true, TerminalRetired)
	w.opts.Capacity = 1
	next := start.Identity
	next.Session = "capacity"
	if _, err := w.Terminal(context.Background(), next, true); !errors.Is(err, ErrUncertain) {
		t.Fatal("capacity supplied absent-execution proof", err)
	}
	if len(w.records) != 1 {
		t.Fatal("terminal API bypassed capacity")
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	w.opts.Capacity = 2
	if _, err := w.Terminal(context.Background(), next, true); !errors.Is(err, ErrUncertain) {
		t.Fatal("closed worker manufactured tombstone proof", err)
	}
	terminalState(t, w, start.Identity, false, TerminalRetired) // Existing actual proof survives Close.
}

func TestTerminalLateStartAndExactReplacementOwnership(t *testing.T) {
	w, f, start := fixture(t)
	f.entered = make(chan struct{})
	f.release = make(chan struct{})
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() { finished <- rpc(w, "start", start, true) }()
	select {
	case <-f.entered:
	case <-time.After(time.Second):
		t.Fatal("start did not reach backend")
	}
	stopped := terminalState(t, w, start.Identity, true, TerminalPending)
	if !stopped.Stopped {
		t.Fatal("late start was not sealed")
	}
	close(f.release)
	select {
	case response := <-finished:
		if response.Code != 409 {
			t.Fatal("late start published")
		}
	case <-time.After(time.Second):
		t.Fatal("late start did not finish")
	}
	if !f.session.stopped {
		t.Fatal("late exact encoder not retired")
	}
	terminalState(t, w, start.Identity, false, TerminalPending)
	close(f.session.done)
	terminalState(t, w, start.Identity, false, TerminalRetired)
	next := start
	next.Identity.Generation += 2
	f.entered, f.release = nil, nil
	if state(t, rpc(w, "start", next, true)) != "running" {
		t.Fatal("proven replacement did not start")
	}
	replacement := f.session
	absentOld := start.Identity
	absentOld.Generation++
	terminalState(t, w, absentOld, true, TerminalRetired)
	if replacement.stopped {
		t.Fatal("absent old stop interfered with replacement")
	}
	terminalState(t, w, next.Identity, false, TerminalRunning)
	terminalState(t, w, start.Identity, true, TerminalRetired)
	if replacement.stopped {
		t.Fatal("old exact stop retired replacement")
	}
	if w.highest[start.Identity.Controller+"/"+start.Identity.Session] != next.Identity.Generation {
		t.Fatal("stale stop regressed generation floor")
	}
}

type terminalInput struct {
	done   chan struct{}
	sealed bool
	err    error
}

func (p *terminalInput) ClaimInput(func(error)) error                      { return nil }
func (p *terminalInput) HeadersForInput(string) (map[string]string, error) { return nil, nil }
func (p *terminalInput) SealInput()                                        { p.sealed = true }
func (p *terminalInput) InputDone() <-chan struct{}                        { return p.done }
func (p *terminalInput) InputErr() error {
	select {
	case <-p.done:
		return p.err
	default:
		return core.ErrEncoderInputPending
	}
}

func TestTerminalRetainsIndependentInputAndUnverifiedProof(t *testing.T) {
	w, f, start := fixture(t)
	if state(t, rpc(w, "start", start, true)) != "running" {
		t.Fatal("start")
	}
	input := &terminalInput{done: make(chan struct{})}
	w.mu.Lock()
	w.records[start.Identity].gatewayInput = input
	w.mu.Unlock()
	terminalState(t, w, start.Identity, true, TerminalPending)
	close(f.session.done)
	terminalState(t, w, start.Identity, false, TerminalPending)
	if !input.sealed {
		t.Fatal("terminal stop lost input seal")
	}
	input.err = core.ErrEncoderInputUnverified
	close(input.done)
	terminalState(t, w, start.Identity, false, TerminalUnverified)
	if rpc(w, "start", Start{Identity: Identity{Controller: start.Identity.Controller, Worker: start.Identity.Worker, Session: start.Identity.Session, Generation: 2}, Grant: start.Grant, Expires: start.Expires, Decision: start.Decision}, true).Code != 409 {
		t.Fatal("unverified input authorized replacement")
	}
}

func TestTerminalInvalidIdentityAndCancellationHaveNoProof(t *testing.T) {
	w, _, start := fixture(t)
	wrong := start.Identity
	wrong.Worker = "other-boot"
	for _, id := range []Identity{wrong, {}, Identity{Controller: "other-controller", Worker: start.Identity.Worker, Session: start.Identity.Session}} {
		if _, err := w.Terminal(context.Background(), id, true); !errors.Is(err, ErrUnsupported) {
			t.Fatal("invalid identity accepted", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.Terminal(ctx, start.Identity, true); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled stop manufactured proof", err)
	}
	if len(w.records) != 0 {
		t.Fatal("invalid terminal request mutated ownership")
	}
}
