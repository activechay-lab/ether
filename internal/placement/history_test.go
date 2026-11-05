package placement

import (
	"context"
	"errors"
	"testing"
	"time"
)

type receiptVerifierFunc func(context.Context, RetirementReceipt) error

func (f receiptVerifierFunc) VerifyReceipt(ctx context.Context, r RetirementReceipt) error {
	return f(ctx, r)
}
func receiptFor(r Record) RetirementReceipt {
	return RetirementReceipt{Key: HistoryKey{Cluster: "cluster", Fence: r.Fence}, Operation: r.Operation, RequestHash: [32]byte{1}, Source: Target{"source", "source-boot"}, PlayID: "play", Revision: "revision", Size: 123, EncoderProof: [32]byte{2}, InputProof: [32]byte{3}, SourceProof: [32]byte{4}, InitialDeadline: r.InitialDeadline, Renewable: true, Renewal: &RenewalSummary{Operation: 1, PreviousDeadline: r.InitialDeadline, Deadline: r.InitialDeadline.Add(time.Second)}}
}
func historyFor(t *testing.T, s *Service, verify receiptVerifierFunc) *History {
	t.Helper()
	h, err := NewHistory(s, "cluster", verify)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func TestHistoryAtomicRetirementReplayAndNamespace(t *testing.T) {
	s, db := fixture(t)
	ctx := context.Background()
	r := mustReserve(t, s, request("history"))
	receipt := receiptFor(r)
	calls := 0
	h := historyFor(t, s, func(_ context.Context, got RetirementReceipt) error {
		calls++
		if got.Key != receipt.Key {
			t.Fatal("wrong verified identity")
		}
		// Mutating a callback copy cannot rewrite the immutable stored summary.
		got.Renewal.Operation = 99
		return nil
	})
	if err := h.Record(ctx, receipt); !errors.Is(err, ErrRetirement) {
		t.Fatalf("active record: %v", err)
	}
	var claims int
	if err := db.QueryRow(`SELECT COUNT(*) FROM placement_claims WHERE session=?`, r.Session).Scan(&claims); err != nil || claims != 2 {
		t.Fatalf("claims %d: %v", claims, err)
	}
	if err := h.RetireWithReceipt(ctx, receipt, "authenticated-retirement"); err != nil {
		t.Fatal(err)
	}
	got, err := h.Lookup(ctx, receipt.Key)
	if err != nil || got.Renewal.Operation != 1 {
		t.Fatalf("receipt %+v: %v", got, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM placement_claims WHERE session=?`, r.Session).Scan(&claims); err != nil || claims != 0 {
		t.Fatalf("claims %d: %v", claims, err)
	}
	next := request(r.Session)
	next.Operation = "replacement"
	next.ExpectedGeneration = r.Generation
	replacement := mustReserve(t, s, next)
	before := calls
	if err := h.Record(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	if err := h.RetireWithReceipt(ctx, receipt, "authenticated-retirement"); err != nil {
		t.Fatal(err)
	}
	if calls != before {
		t.Fatal("historical replay depended on discarded payload")
	}
	changed := receipt
	changed.PlayID = "other"
	if err := h.Record(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed replay: %v", err)
	}
	current, err := s.Lookup(ctx, r.Session)
	if err != nil || current.Fence != replacement.Fence || current.Retired {
		t.Fatalf("replacement affected: %+v %v", current, err)
	}
	wrong := receipt.Key
	wrong.Fence.Controller.ID = "other"
	if _, err = h.Lookup(ctx, wrong); !errors.Is(err, ErrReceiptUnknown) {
		t.Fatalf("wrong exact identity: %v", err)
	}
	other, err := NewHistory(s, "other-cluster", receiptVerifierFunc(func(context.Context, RetirementReceipt) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	wrong = receipt.Key
	wrong.Cluster = "other-cluster"
	if _, err = other.Lookup(ctx, wrong); !errors.Is(err, ErrPolicy) {
		t.Fatalf("namespace: %v", err)
	}
	used, retired, err := s.LedgerUsage(ctx)
	if err != nil || used != 1 || retired != 0 {
		t.Fatalf("floor lost: %d/%d %v", used, retired, err)
	}
}
func TestHistoryReentrantVerificationCannotRetireReplacement(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	r := mustReserve(t, s, request("race"))
	h := historyFor(t, s, func(context.Context, RetirementReceipt) error {
		if err := s.Retire(ctx, r.Fence, "authenticated-retirement"); err != nil {
			return err
		}
		next := request(r.Session)
		next.Operation = "replacement"
		next.ExpectedGeneration = r.Generation
		_, err := s.Reserve(ctx, next)
		return err
	})
	if err := h.RetireWithReceipt(ctx, receiptFor(r), "authenticated-retirement"); !errors.Is(err, ErrFenced) {
		t.Fatalf("stale verifier: %v", err)
	}
	current, err := s.Lookup(ctx, r.Session)
	if err != nil || current.Generation != 2 || current.Retired {
		t.Fatalf("replacement: %+v %v", current, err)
	}
	if _, err = h.Lookup(ctx, receiptFor(r).Key); !errors.Is(err, ErrReceiptUnknown) {
		t.Fatalf("invented old proof: %v", err)
	}
}
func TestHistoryDeniedAndUnknownOutcomeRetainResponsibility(t *testing.T) {
	s, db := fixture(t)
	r := mustReserve(t, s, request("unknown"))
	receipt := receiptFor(r)
	h := historyFor(t, s, func(context.Context, RetirementReceipt) error { return ErrRetirement })
	if err := h.RetireWithReceipt(context.Background(), receipt, "authenticated-retirement"); !errors.Is(err, ErrRetirement) {
		t.Fatalf("proof denial: %v", err)
	}
	if err := h.RetireWithReceipt(context.Background(), receipt, "wrong-evidence"); !errors.Is(err, ErrRetirement) {
		t.Fatalf("existing verifier bypass: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h = historyFor(t, s, func(context.Context, RetirementReceipt) error { cancel(); return nil })
	var unknown *UnknownOutcome
	if err := h.RetireWithReceipt(ctx, receipt, "authenticated-retirement"); !errors.As(err, &unknown) {
		t.Fatalf("expected unknown: %v", err)
	}
	if _, err := h.Lookup(context.Background(), receipt.Key); !errors.Is(err, ErrReceiptUnknown) {
		t.Fatalf("false confirmation: %v", err)
	}
	current, err := s.Lookup(context.Background(), r.Session)
	if err != nil || current.Retired {
		t.Fatalf("retired despite failure: %+v %v", current, err)
	}
	var claims int
	if err := db.QueryRow(`SELECT COUNT(*) FROM placement_claims WHERE session=?`, r.Session).Scan(&claims); err != nil || claims != 2 {
		t.Fatalf("claims %d: %v", claims, err)
	}
}
