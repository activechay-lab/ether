package remotetranscode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestTerminalEvidencePositiveHashRenewalAndIndependentProofs(t *testing.T) {
	w, f, start := fixture(t)
	w.opts.ValidateRenewal = receipt
	if state(t, rpc(w, "start", start, true)) != "running" {
		t.Fatal("start")
	}
	n := renewal(start)
	if renewRPC(w, n).Code != 200 {
		t.Fatal("renew")
	}
	terminalState(t, w, start.Identity, true, TerminalPending)
	// Returned Start token and session-owned token must BOTH prove retirement.
	owned := f.session
	backend := &evidenceToken{done: make(chan struct{})}
	w.mu.Lock()
	w.records[start.Identity].retirement = backend
	w.mu.Unlock()
	close(owned.done)
	if _, err := w.TerminalEvidence(context.Background(), start.Identity); !errors.Is(err, ErrUncertain) {
		t.Fatal("session proof substituted for pending Start token", err)
	}
	close(backend.done)
	summary, err := w.TerminalEvidence(context.Background(), start.Identity)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(start)
	if summary.StartHash != sha256.Sum256(b) || summary.Origin != EvidenceAcceptedStart || !summary.BackendInvoked || !summary.BackendTokenJoined || !summary.OwnedSessionTokenJoined || summary.LeaseVersion != 1 || summary.AcceptedRenewal == nil || !sameRenewal(*summary.AcceptedRenewal, n) || !summary.InitialDeadline.Equal(start.Expires) || !summary.LeaseDeadline.Equal(n.Deadline) {
		t.Fatal("summary lost immutable Start/accepted CAS provenance")
	}
	raw, _ := json.Marshal(summary)
	if bytes.Contains(raw, []byte(start.Grant)) || bytes.Contains(raw, []byte("https://")) {
		t.Fatal("terminal evidence leaked source capabilities")
	}
	whole, err := summary.Digest()
	if err != nil {
		t.Fatal(err)
	}
	encoder, _ := summary.EncoderDigest()
	input, _ := summary.InputDigest()
	if whole == ([32]byte{}) || whole == encoder || whole == input || encoder == input {
		t.Fatal("evidence domains collapsed")
	}
	summary.AcceptedRenewal.Deadline = summary.AcceptedRenewal.Deadline.Add(time.Hour)
	again, err := w.TerminalEvidence(context.Background(), start.Identity)
	if err != nil || !sameRenewal(*again.AcceptedRenewal, n) {
		t.Fatal("caller mutated retained renewal evidence")
	}
	stable, _ := again.Digest()
	if whole != stable {
		t.Fatal("terminal evidence digest was not immutable")
	}
	// Existing renewal replay remains observational history and cannot alter evidence.
	if renewRPC(w, n).Code != 200 {
		t.Fatal("accepted renewal replay changed")
	}
	after, _ := w.TerminalEvidence(context.Background(), start.Identity)
	digest, _ := after.Digest()
	if digest != whole {
		t.Fatal("replay mutated terminal evidence")
	}
}

type evidenceToken struct {
	done  chan struct{}
	err   error
	check func()
}

func (p *evidenceToken) EncoderDone() <-chan struct{} { return p.done }
func (p *evidenceToken) EncoderErr() error {
	if p.check != nil {
		p.check()
	}
	select {
	case <-p.done:
		return p.err
	default:
		return core.ErrEncoderPending
	}
}

func TestTerminalEvidenceNegativeIsExplicitAndReadOnly(t *testing.T) {
	for _, host := range []bool{true, false} {
		t.Run(map[bool]string{true: "host", false: "http"}[host], func(t *testing.T) {
			w, _, start := fixture(t)
			if _, err := w.TerminalEvidence(context.Background(), start.Identity); !errors.Is(err, ErrUncertain) {
				t.Fatal("absence became evidence", err)
			}
			if len(w.records) != 0 {
				t.Fatal("evidence serialization installed a barrier")
			}
			if host {
				terminalState(t, w, start.Identity, true, TerminalRetired)
			} else {
				if state(t, rpc(w, "stop", start, true)) != "retired" {
					t.Fatal("stop")
				}
			}
			summary, err := w.TerminalEvidence(context.Background(), start.Identity)
			if err != nil {
				t.Fatal(err)
			}
			if summary.Origin != EvidenceStopBeforeStart || summary.NegativeBarrierGeneration != start.Identity.Generation || summary.StartHash != ([32]byte{}) || !summary.InitialDeadline.IsZero() || summary.AcceptedRenewal != nil || summary.EncoderProof != TerminalProofNegativeBarrier || summary.InputProof != TerminalProofNegativeBarrier || summary.BackendInvoked {
				t.Fatal("negative barrier fabricated Start/object proof")
			}
			digest, err := summary.Digest()
			if err != nil || digest == ([32]byte{}) {
				t.Fatal("negative commitment missing", err)
			}
			if rpc(w, "start", start, true).Code != 409 {
				t.Fatal("evidence disturbed permanent start fence")
			}
		})
	}
}

func TestTerminalEvidenceJoinsEveryValidationExit(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "stopped-after-validating", true: "callback-denied"}[reject], func(t *testing.T) {
			w, f, start := fixture(t)
			entered, release := make(chan struct{}), make(chan struct{})
			w.opts.ValidateRenewal = func(ctx context.Context, peer string, s Start, n Renewal) (RenewalReceipt, error) {
				close(entered)
				<-release
				if reject {
					return RenewalReceipt{}, errors.New("denied")
				}
				return receipt(ctx, peer, s, n)
			}
			if state(t, rpc(w, "start", start, true)) != "running" {
				t.Fatal("start")
			}
			finished := make(chan *httptest.ResponseRecorder, 1)
			go func() { finished <- renewRPC(w, renewal(start)) }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("callback not reached")
			}
			terminalState(t, w, start.Identity, true, TerminalPending)
			close(f.session.done)
			// Old Terminal still reports local resource retirement; stronger evidence
			// intentionally waits for the authority-extending callback to finish.
			terminalState(t, w, start.Identity, false, TerminalRetired)
			if _, err := w.TerminalEvidence(context.Background(), start.Identity); !errors.Is(err, ErrUncertain) {
				t.Fatal("proof escaped while source renewal callback could extend authority", err)
			}
			close(release)
			select {
			case response := <-finished:
				if response.Code != 409 && response.Code != 403 {
					t.Fatal("stopped validation committed", response.Code)
				}
			case <-time.After(time.Second):
				t.Fatal("validation did not join")
			}
			summary, err := w.TerminalEvidence(context.Background(), start.Identity)
			if err != nil || summary.LeaseVersion != 0 || summary.AcceptedRenewal != nil {
				t.Fatal("source-only callback became accepted renewal history", err)
			}
		})
	}
}

func TestTerminalEvidenceRejectsFailedStartAndUnverifiedInputs(t *testing.T) {
	w, _, start := fixture(t)
	w.opts.Resolve = func(context.Context, string, Start) (Source, error) { return Source{}, errors.New("no source") }
	if rpc(w, "start", start, true).Code != 409 {
		t.Fatal("source error")
	}
	terminalState(t, w, start.Identity, false, TerminalRetired)
	if _, err := w.TerminalEvidence(context.Background(), start.Identity); !errors.Is(err, ErrUnsupported) {
		t.Fatal("accepted pre-backend failure relabeled NeverStarted", err)
	}
	w2, f, start2 := fixture(t)
	if state(t, rpc(w2, "start", start2, true)) != "running" {
		t.Fatal("start")
	}
	input := &terminalInput{done: make(chan struct{})}
	w2.mu.Lock()
	w2.records[start2.Identity].gatewayInput = input
	w2.mu.Unlock()
	terminalState(t, w2, start2.Identity, true, TerminalPending)
	close(f.session.done)
	if _, err := w2.TerminalEvidence(context.Background(), start2.Identity); !errors.Is(err, ErrUncertain) {
		t.Fatal("pending input supplied evidence", err)
	}
	input.err = core.ErrEncoderInputUnverified
	close(input.done)
	if _, err := w2.TerminalEvidence(context.Background(), start2.Identity); !errors.Is(err, ErrUncertain) {
		t.Fatal("nonnil terminal input supplied evidence", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w2.TerminalEvidence(ctx, start2.Identity); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled evidence supplied proof", err)
	}
}

func TestTerminalEvidenceProofGettersRunOutsideWorkerMutex(t *testing.T) {
	w, f, start := fixture(t)
	if state(t, rpc(w, "start", start, true)) != "running" {
		t.Fatal("start")
	}
	terminalState(t, w, start.Identity, true, TerminalPending)
	close(f.session.done)
	backend := &evidenceToken{done: make(chan struct{}), check: func() { w.mu.Lock(); w.mu.Unlock() }} //nolint:staticcheck // barrier: check must run with w.mu free
	close(backend.done)
	w.mu.Lock()
	w.records[start.Identity].retirement = backend
	w.mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := w.TerminalEvidence(context.Background(), start.Identity); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("evidence invoked proof getter under worker mutex")
	}
}

func TestTerminalEvidenceRejectsEncoderErrorAndMidProofCancellation(t *testing.T) {
	for _, cancelDuringProof := range []bool{false, true} {
		t.Run(map[bool]string{false: "encoder-error", true: "cancel-during-proof"}[cancelDuringProof], func(t *testing.T) {
			w, f, start := fixture(t)
			if state(t, rpc(w, "start", start, true)) != "running" {
				t.Fatal("start")
			}
			terminalState(t, w, start.Identity, true, TerminalPending)
			close(f.session.done)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend := &evidenceToken{done: make(chan struct{})}
			if cancelDuringProof {
				backend.check = cancel
			} else {
				backend.err = core.ErrEncoderInputUnverified
			}
			close(backend.done)
			w.mu.Lock()
			w.records[start.Identity].retirement = backend
			w.mu.Unlock()
			summary, err := w.TerminalEvidence(ctx, start.Identity)
			if err == nil || summary.Version != 0 {
				t.Fatal("canceled/unverified proof returned evidence")
			}
			if cancelDuringProof && !errors.Is(err, context.Canceled) {
				t.Fatal("mid-proof cancellation ignored", err)
			}
		})
	}
}
