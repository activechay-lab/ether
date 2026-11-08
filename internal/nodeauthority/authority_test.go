package nodeauthority

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodesource"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/playback"
)

type lookupFixture struct {
	record placement.Record
	err    error
	during func()
}

func (f *lookupFixture) Lookup(context.Context, string) (placement.Record, error) {
	if f.during != nil {
		f.during()
	}
	return f.record, f.err
}

// Neutral exact-handle fixture exercises callback and error ownership boundaries;
// playback's separate SourceAccess fixture exercises the real manager barrier.
type sourceFixture struct {
	active  bool
	acquire func(context.Context) (playback.SourceHold, error)
}

func (*sourceFixture) Revision() string { return "revision" }
func (*sourceFixture) Size() int64      { return 10 }
func (f *sourceFixture) Active() bool   { return f.active }
func (f *sourceFixture) Revoke()        { f.active = false }
func (f *sourceFixture) Acquire(ctx context.Context) (playback.SourceHold, error) {
	if !f.active {
		return playback.SourceHold{}, ErrDenied
	}
	return f.acquire(ctx)
}
func fixture(t *testing.T) (*Authority, *lookupFixture, *sourceFixture, Handle, *time.Time) {
	t.Helper()
	now := time.Unix(1800000000, 0)
	s := nodesource.Scope{Fence: placement.Fence{Session: "session", Generation: math.MaxInt64, Controller: placement.Controller{ID: "controller", Boot: "cb"}, Target: placement.Target{NodeID: "worker", Boot: "wb"}}, Viewer: "viewer", Revision: "revision"}
	db := &lookupFixture{record: placement.Record{Fence: s.Fence, Role: config.RoleTranscode, Authority: true, Deadline: now.Add(time.Minute)}}
	a, err := New(Options{Lookup: db, Policy: func(context.Context, placement.Target, nodesource.Scope) error { return nil }, Owner: placement.Target{NodeID: "source", Boot: "sb"}, Now: func() time.Time { return now }, ClockBoundKnown: true, ClockSkew: time.Second, MaxLifetime: time.Minute, MaxBindings: 1, MaxGrants: 2, MaxGrantsPerBinding: 2})
	if err != nil {
		t.Fatal(err)
	}
	src := &sourceFixture{active: true}
	h, err := a.bind(context.Background(), s, src, 10)
	if err != nil {
		t.Fatal(err)
	}
	return a, db, src, h, &now
}
func TestAuthorityFenceRotationAndBounds(t *testing.T) {
	a, db, src, h, now := fixture(t)
	ctx := context.Background()
	old, err := a.Issue(ctx, h, now.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	g := old.Grant()
	p := nodesource.Peer{NodeID: "worker", Boot: "wb"}
	resolved, scope, size, err := a.Resolve(ctx, p, h.Value())
	if err != nil || resolved != h || scope != g.Scope || size != 10 {
		t.Fatal("stable resolution")
	}
	if _, _, _, err = a.Resolve(ctx, nodesource.Peer{NodeID: "worker", Boot: "other"}, h.Value()); err == nil {
		t.Fatal("resolve peer bypass")
	}

	if _, err = a.Verify(ctx, p, g.Credential); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []nodesource.Peer{{NodeID: "other", Boot: "wb"}, {NodeID: "worker", Boot: "other"}} {
		if _, err = a.Verify(ctx, bad, g.Credential); err == nil {
			t.Fatal("peer bypass")
		}
	}
	encoded, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip nodesource.Grant
	if err = json.Unmarshal(encoded, &roundtrip); err != nil {
		t.Fatal(err)
	}
	next, err := a.Renew(ctx, h, roundtrip, now.Add(20*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if next.Grant().Credential == g.Credential {
		t.Fatal("bearer reused")
	}
	if checked, err := a.Verify(ctx, p, g.Credential); err != nil || checked != g {
		t.Fatal("old grant expiry changed")
	}
	if _, err = a.Renew(ctx, h, g, now.Add(30*time.Second)); err == nil {
		t.Fatal("renew replay")
	}
	if _, err = a.Renew(ctx, h, next.Grant(), now.Add(30*time.Second)); !errors.Is(err, ErrCapacity) {
		t.Fatal("old grant bound", err)
	}
	*now = now.Add(11 * time.Second)
	if _, err = a.Verify(ctx, p, g.Credential); err == nil {
		t.Fatal("old grant extended")
	}
	third, err := a.Renew(ctx, h, next.Grant(), now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Renew(ctx, h, third.Grant(), db.record.Deadline); err == nil {
		t.Fatal("deadline skew bypass")
	}
	original := db.record
	db.record.Controller.Boot = "new"
	if _, err = a.Verify(ctx, p, third.Grant().Credential); err == nil {
		t.Fatal("stale fence")
	}
	db.record = original
	db.err = errors.New("secret upstream detail")
	if _, err = a.Verify(ctx, p, third.Grant().Credential); err != ErrDenied {
		t.Fatal("lookup fail-open/error leak")
	}
	db.err = nil
	for _, mutate := range []func(){func() { db.record.Retired = true }, func() { db.record.Authority = false }, func() { db.record.Role = config.RoleSource }, func() { src.active = false }} {
		mutate()
		if _, err = a.Verify(ctx, p, third.Grant().Credential); err == nil {
			t.Fatal("stale authority")
		}
		db.record = original
		src.active = true
	}
	for _, v := range []any{a, h, old} {
		if strings.Contains(fmt.Sprintf("%+v %#v", v, v), g.Credential) || strings.Contains(fmt.Sprintf("%+v %#v", v, v), h.Value()) {
			t.Fatal("format leaked secret")
		}
	}
	if err = a.Revoke(h); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Verify(ctx, p, third.Grant().Credential); err == nil {
		t.Fatal("revoked verify")
	}
	if _, _, _, err = a.Resolve(ctx, p, h.Value()); err == nil {
		t.Fatal("revoked resolution")
	}

	if _, err = a.bind(ctx, originalScope(original), &sourceFixture{active: true}, 10); err == nil {
		t.Fatal("binding churn")
	}
}
func originalScope(r placement.Record) nodesource.Scope {
	return nodesource.Scope{Fence: r.Fence, Viewer: "viewer", Revision: "revision"}
}
func TestCallbackRevokeAndErrorHold(t *testing.T) {
	a, db, src, h, now := fixture(t)
	ctx := core.WithReadRetirement(context.Background(), &core.ReadRetirement{})
	lease, err := a.Issue(ctx, h, now.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	db.during = func() { _ = a.Revoke(h) }
	if _, err = a.Verify(ctx, nodesource.Peer{NodeID: "worker", Boot: "wb"}, lease.Grant().Credential); err == nil {
		t.Fatal("callback revoke passed verify")
	}
	a, db, src, h, _ = fixture(t)
	released := false
	src.acquire = func(context.Context) (playback.SourceHold, error) {
		_ = a.Revoke(h)
		return playback.SourceHold{Release: func() { released = true }}, nil
	}
	hold, err := a.Acquire(ctx, originalScope(db.record))
	if err == nil || hold.Source != nil || hold.Release == nil || released {
		t.Fatal("revocation lost hold ownership")
	}
	hold.Release()
	a, db, src, _, _ = fixture(t)
	src.acquire = func(context.Context) (playback.SourceHold, error) {
		return playback.SourceHold{Release: func() { released = true }}, errors.New("secret")
	}
	hold, err = a.Acquire(ctx, originalScope(db.record))
	if err != ErrDenied || hold.Release == nil {
		t.Fatal("acquisition error lost hold")
	}
	hold.Release()
}

// One source boot binds far more than MaxBindings sources over its lifetime:
// sealed bindings whose fence placement has retired are forgotten, so binding
// never fails for capacity and retained state stays flat. A sealed binding
// whose fence is still authoritative is kept, and a forgotten scope can never
// be bound again because placement never re-authorises a retired fence.
func TestForgetBoundsLifetimeBindings(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1800000000, 0)
	db := &lookupFixture{}
	a, err := New(Options{Lookup: db, Policy: func(context.Context, placement.Target, nodesource.Scope) error { return nil }, Owner: placement.Target{NodeID: "source", Boot: "sb"}, Now: func() time.Time { return now }, ClockBoundKnown: true, ClockSkew: time.Second, MaxLifetime: time.Minute, MaxBindings: 4, MaxGrants: 4, MaxGrantsPerBinding: 1})
	if err != nil {
		t.Fatal(err)
	}
	scope := func(i int) nodesource.Scope {
		return nodesource.Scope{Fence: placement.Fence{Session: fmt.Sprintf("s%d", i), Generation: 1, Controller: placement.Controller{ID: "controller", Boot: "cb"}, Target: placement.Target{NodeID: "worker", Boot: "wb"}}, Viewer: "viewer", Revision: "revision"}
	}
	live := func(s nodesource.Scope) placement.Record {
		return placement.Record{Fence: s.Fence, Role: config.RoleTranscode, Authority: true, Deadline: now.Add(time.Minute)}
	}
	for i := 0; i < 10000; i++ {
		s := scope(i)
		db.record = live(s)
		h, err := a.bind(ctx, s, &sourceFixture{active: true}, 10)
		if err != nil {
			t.Fatalf("bind %d refused: %v", i, err)
		}
		if err = a.Revoke(h); err != nil {
			t.Fatal(err)
		}
		db.record.Retired, db.record.Authority = true, false
		a.Forget(ctx, 0, 64)
		if n := a.Bindings(); n != 0 {
			t.Fatalf("bind %d: %d bindings retained", i, n)
		}
		if err = a.Revoke(h); err != nil {
			t.Fatalf("revoking a forgotten handle: %v", err)
		}
	}
	// A forgotten scope stays unbindable: its fence is retired.
	if _, err = a.bind(ctx, scope(0), &sourceFixture{active: true}, 10); err == nil {
		t.Fatal("forgotten retired scope bound again")
	}
	// Sealed but still authoritative (for example a lifetime-capped binding of
	// a live session): retained, so the scope cannot be rebound.
	s := scope(-1)
	db.record = live(s)
	h, err := a.bind(ctx, s, &sourceFixture{active: true}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Revoke(h); err != nil {
		t.Fatal(err)
	}
	if a.Forget(ctx, 0, 64) != 0 || a.Bindings() != 1 {
		t.Fatal("forgot a binding whose fence is still authoritative")
	}
	if _, err = a.bind(ctx, s, &sourceFixture{active: true}, 10); err == nil {
		t.Fatal("sealed live scope rebound")
	}
	// The grace period is honoured.
	db.record.Retired = true
	if a.Forget(ctx, time.Hour, 64) != 0 {
		t.Fatal("forgot inside the grace period")
	}
}
