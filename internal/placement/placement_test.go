package placement

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/store"
)

type verifierFunc func(context.Context, Fence, string) error

func (f verifierFunc) VerifyRetired(ctx context.Context, g Fence, p string) error {
	return f(ctx, g, p)
}
func fixture(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db := store.OpenTest(t)
	s, err := New(db, Options{MaxSessions: 32, MaxLease: time.Minute, LockTimeout: 5 * time.Second}, verifierFunc(func(_ context.Context, _ Fence, p string) error {
		if p != "authenticated-retirement" {
			return ErrRetirement
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, scope := range []string{"gpu", "streams"} {
		if err = s.SetPool(ctx, scope, config.RoleTranscode, 2); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.SetTarget(ctx, Target{"worker", "boot-1"}, config.NodeRoles{config.RoleTranscode}, map[config.NodeRole][]string{config.RoleTranscode: {"gpu", "streams"}}); err != nil {
		t.Fatal(err)
	}
	return s, db
}
func request(session string) Request {
	return Request{Session: session, Operation: "start", Controller: Controller{"coordinator", "cb1"}, Target: Target{"worker", "boot-1"}, Role: config.RoleTranscode, Claims: []Claim{{"gpu", 1}, {"streams", 1}}, Lease: 10 * time.Second}
}
func mustReserve(t *testing.T, s *Service, r Request) Record {
	t.Helper()
	got, err := s.Reserve(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func TestConcurrentAdmissionAndReplay(t *testing.T) {
	s, db := fixture(t)
	other, _ := New(db, s.options, s.verifier)
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			service := s
			if i%2 == 0 {
				service = other
			}
			_, err := service.Reserve(context.Background(), request(fmt.Sprint(i)))
			if err == nil {
				admitted.Add(1)
			} else if !errors.Is(err, ErrCapacity) {
				t.Errorf("reserve: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if admitted.Load() != 2 {
		t.Fatalf("admitted %d", admitted.Load())
	}
	// Separate coordinators replay one operation concurrently without doubling use.
	s2, _ := fixture(t)
	r := request("same")
	first := mustReserve(t, s2, r)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s2.Reserve(context.Background(), r)
			if err != nil || got.Fence != first.Fence || !got.Deadline.Equal(first.Deadline) {
				t.Errorf("replay: %+v %v", got, err)
			}
		}()
	}
	wg.Wait()
	var units int64
	if err := s2.db.QueryRow(`SELECT SUM(units) FROM placement_claims WHERE scope='gpu'`).Scan(&units); err != nil || units != 1 {
		t.Fatalf("units=%d err=%v", units, err)
	}
}
func TestFencingRetirementAndImmutableReplay(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	req := request("session")
	first := mustReserve(t, s, req)
	changed := req
	changed.Controller.Boot = "changed"
	if _, err := s.Reserve(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	next := req
	next.Operation = "replace"
	next.ExpectedGeneration = 1
	if _, err := s.Reserve(ctx, next); !errors.Is(err, ErrRetirement) {
		t.Fatal(err)
	}
	if err := s.Renew(ctx, first.Fence, first.Deadline.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	replay := mustReserve(t, s, req)
	if !replay.Deadline.Equal(first.Deadline) {
		t.Fatal("replay extended authority")
	}
	if err := s.Retire(ctx, first.Fence, "bad"); !errors.Is(err, ErrRetirement) {
		t.Fatal(err)
	}
	if err := s.Retire(ctx, first.Fence, "authenticated-retirement"); err != nil {
		t.Fatal(err)
	}
	replay = mustReserve(t, s, req)
	if !replay.Retired || replay.Authority {
		t.Fatal("terminal replay resurrected")
	}
	replacement := mustReserve(t, s, next)
	if replacement.Generation != 2 {
		t.Fatal(replacement)
	}
	if _, err := s.Reserve(ctx, req); !errors.Is(err, ErrFenced) {
		t.Fatal(err)
	}
	if err := s.Renew(ctx, first.Fence, time.Now().Add(time.Second)); !errors.Is(err, ErrFenced) {
		t.Fatal(err)
	}
	if err := s.Retire(ctx, first.Fence, "authenticated-retirement"); !errors.Is(err, ErrFenced) {
		t.Fatal(err)
	}
	got, err := s.Lookup(ctx, "session")
	if err != nil || got.Generation != 2 || len(got.Claims) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestExpiryPolicyAndQuota(t *testing.T) {
	s, db := fixture(t)
	ctx := context.Background()
	first := mustReserve(t, s, request("expired"))
	// Simulate DB-clock expiry without sleeping. No production expiry bypass exists.
	if _, err := db.Exec(`UPDATE placement_sessions SET deadline=1 WHERE session=?`, first.Session); err != nil {
		t.Fatal(err)
	}
	got, err := s.Lookup(ctx, first.Session)
	if err != nil || got.Authority || got.Retired {
		t.Fatalf("%+v %v", got, err)
	}
	if err = s.Renew(ctx, first.Fence, time.Now().Add(time.Second)); !errors.Is(err, ErrFenced) {
		t.Fatal(err)
	}
	mustReserve(t, s, request("second"))
	if _, err = s.Reserve(ctx, request("full")); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	// Restart admission changes boot policy, never clears old reservations.
	if err = s.SetTarget(ctx, Target{"worker", "boot-2"}, config.NodeRoles{config.RoleTranscode}, map[config.NodeRole][]string{config.RoleTranscode: {"gpu", "streams"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Reserve(ctx, request("old-boot")); !errors.Is(err, ErrPolicy) {
		t.Fatal(err)
	}
	r := request("new-boot")
	r.Target.Boot = "boot-2"
	if _, err = s.Reserve(ctx, r); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	s.verifier = nil
	if err = s.Retire(ctx, first.Fence, "authenticated-retirement"); !errors.Is(err, ErrRetirement) {
		t.Fatal(err)
	}
	s.verifier = verifierFunc(func(context.Context, Fence, string) error { return nil })
	if err = s.Retire(ctx, first.Fence, "authenticated-retirement"); err != nil {
		t.Fatal(err)
	}
	// The quota bounds live generations ("second"); the retired tombstone
	// never refuses admission.
	s.options.MaxSessions = 1
	if _, err = s.Reserve(ctx, r); !errors.Is(err, ErrQuota) {
		t.Fatal(err)
	}
	used, terminal, err := s.LedgerUsage(ctx)
	if err != nil || used != 2 || terminal != 1 {
		t.Fatalf("%d %d %v", used, terminal, err)
	}
	s.options.MaxSessions = 2
	if _, err = s.Reserve(ctx, r); err != nil {
		t.Fatalf("dead tombstone refused admission: %v", err)
	}
}
func TestAtomicScopesAndAuthoritativePolicy(t *testing.T) {
	s, db := fixture(t)
	ctx := context.Background()
	if err := s.SetPool(ctx, "streams", config.RoleTranscode, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reserve(ctx, request("no-space")); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM placement_claims`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial claims %d %v", count, err)
	}
	r := request("omit")
	r.Claims = r.Claims[:1]
	if _, err := s.Reserve(ctx, r); !errors.Is(err, ErrPolicy) {
		t.Fatal(err)
	}
	r = request("duplicate")
	r.Claims = append(r.Claims, r.Claims[0])
	if _, err := s.Reserve(ctx, r); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	r = request("missing")
	r.Claims[0].Scope = "unknown"
	if _, err := s.Reserve(ctx, r); !errors.Is(err, ErrPolicy) {
		t.Fatal(err)
	}
	r = request("role")
	r.Role = config.RoleSource
	if _, err := s.Reserve(ctx, r); !errors.Is(err, ErrPolicy) {
		t.Fatal(err)
	}
}
func TestProofOutsideLockAndRechecked(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	req := request("reentrant")
	old := mustReserve(t, s, req)
	original := s.verifier
	s.verifier = verifierFunc(func(ctx context.Context, f Fence, p string) error {
		// A different coordinator retires and replaces while verification is in flight.
		other, _ := New(s.db, s.options, original)
		if err := other.Retire(ctx, f, p); err != nil {
			return err
		}
		req.Operation = "replacement"
		req.ExpectedGeneration = f.Generation
		_, err := other.Reserve(ctx, req)
		return err
	})
	if err := s.Retire(ctx, old.Fence, "authenticated-retirement"); !errors.Is(err, ErrFenced) {
		t.Fatal(err)
	}
	current, err := s.Lookup(ctx, old.Session)
	if err != nil || current.Retired || current.Generation != 2 {
		t.Fatalf("%+v %v", current, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = s.Reserve(canceled, request("cancelled"))
	var unknown *UnknownOutcome
	if !errors.As(err, &unknown) {
		t.Fatalf("not unknown: %v", err)
	}
}

// Separate SQLite handles/pools must serialize at the database, not just inside
// one sql.DB pool. PostgreSQL runs the concurrency test above with four sessions.
func TestSQLiteIndependentCoordinators(t *testing.T) {
	if store.DialectOf(store.OpenTest(t)) == store.Postgres {
		t.Skip("SQLite-specific handle test; PostgreSQL concurrent sessions covered above")
	}
	ctx := context.Background()
	path := t.TempDir() + "/placement.db"
	a, err := store.OpenPath(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := store.OpenPath(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	opts := Options{MaxSessions: 32, MaxLease: time.Minute, LockTimeout: 5 * time.Second}
	first, _ := New(a, opts, nil)
	second, _ := New(b, opts, nil)
	if err = first.SetPool(ctx, "gpu", config.RoleTranscode, 1); err != nil {
		t.Fatal(err)
	}
	if err = first.SetTarget(ctx, Target{"worker", "boot-1"}, config.NodeRoles{config.RoleTranscode}, map[config.NodeRole][]string{config.RoleTranscode: {"gpu"}}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var admitted atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			s := first
			if i%2 == 0 {
				s = second
			}
			r := request(fmt.Sprint(i))
			r.Claims = r.Claims[:1]
			_, err := s.Reserve(ctx, r)
			if err == nil {
				admitted.Add(1)
			} else if !errors.Is(err, ErrCapacity) {
				t.Errorf("reserve: %v", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if admitted.Load() != 1 {
		t.Fatalf("admitted %d", admitted.Load())
	}
}
