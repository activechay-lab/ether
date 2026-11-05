package placement

import (
	"context"
	"errors"
	"testing"
)

func TestFleetQuotaPolicyAndStaleCoordinator(t *testing.T) {
	ctx := context.Background()
	s, db := fixture(t)
	if err := s.EnsureLedgerQuota(ctx); err != nil {
		t.Fatal(err)
	}
	original := mustReserve(t, s, request("original"))
	opts := s.options
	opts.MaxSessions = 64
	larger, err := New(db, opts, s.verifier)
	if err != nil {
		t.Fatal(err)
	}
	if err = larger.EnsureLedgerQuota(ctx); !errors.Is(err, ErrQuotaPolicy) {
		t.Fatalf("silently raised policy: %v", err)
	}
	if _, err = larger.Reserve(ctx, request("larger")); !errors.Is(err, ErrQuotaPolicy) {
		t.Fatalf("inconsistent coordinator allocated: %v", err)
	}
	if err = larger.RaiseLedgerQuota(ctx, 31); !errors.Is(err, ErrQuotaPolicy) {
		t.Fatalf("stale operator CAS accepted: %v", err)
	}
	if err = larger.RaiseLedgerQuota(ctx, 32); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Reserve(ctx, request("stale")); !errors.Is(err, ErrQuotaPolicy) {
		t.Fatalf("old coordinator allocated after raise: %v", err)
	}
	if got, err := s.Reserve(ctx, request("original")); err != nil || got.Fence != original.Fence {
		t.Fatalf("existing replay blocked: %v", err)
	}
	if err = s.Retire(ctx, original.Fence, "authenticated-retirement"); err != nil {
		t.Fatalf("retirement blocked after policy change: %v", err)
	}
	status, err := larger.LedgerStatus(ctx)
	if err != nil || status.Capacity != 64 || status.Used != 1 || status.Retired != 1 || !status.Matches || status.Exhausted {
		t.Fatalf("quota observation %+v %v", status, err)
	}
	if err = larger.EnsureLedgerQuota(ctx); err != nil {
		t.Fatal(err)
	}
}
