package cluster

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/store"
)

type fakeClockDB struct {
	wall, mono, db time.Time
	unavailable    bool
}

func (f *fakeClockDB) Connect(context.Context) (driver.Conn, error) { return fakeClockConn{f}, nil }
func (f *fakeClockDB) Driver() driver.Driver                        { return fakeClockDriver{} }

type fakeClockDriver struct{}

func (fakeClockDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type fakeClockConn struct{ f *fakeClockDB }

func (fakeClockConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unsupported") }
func (fakeClockConn) Begin() (driver.Tx, error)           { return nil, errors.New("unsupported") }
func (fakeClockConn) Close() error                        { return nil }
func (c fakeClockConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if !strings.Contains(query, "clock_timestamp()") {
		return nil, errors.New("unexpected query")
	}
	if c.f.unavailable {
		return nil, errors.New("fake outage")
	}
	// The server samples at the midpoint of a 2ms round trip.
	value := c.f.db.Add(time.Millisecond).UnixMicro()
	c.f.wall = c.f.wall.Add(2 * time.Millisecond)
	c.f.mono = c.f.mono.Add(2 * time.Millisecond)
	c.f.db = c.f.db.Add(2 * time.Millisecond)
	return &fakeClockRows{value: value}, nil
}

type fakeClockRows struct {
	value int64
	done  bool
}

func (*fakeClockRows) Columns() []string { return []string{"clock"} }
func (*fakeClockRows) Close() error      { return nil }
func (r *fakeClockRows) Next(values []driver.Value) error {
	if r.done {
		return io.EOF
	}
	values[0] = r.value
	r.done = true
	return nil
}

// This invariant follows a node's clock through drift, a backwards step, an
// outage and recovery, exercising the production query and real authority gate.
func TestContinuousClockAuthorityInvariant(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	f := &fakeClockDB{wall: now.Round(0), mono: now, db: now.Round(0)}
	db := sql.OpenDB(f)
	defer db.Close()
	var logs strings.Builder
	p, err := NewPostgres(Options{DB: db, Local: Local{Identity: Identity{NodeID: "clock-node"}}, Logger: slog.New(slog.NewTextHandler(&logs, nil)), Now: func() time.Time { return f.wall }, MonotonicNow: func() time.Time { return f.mono }, ClockBoundKnown: true, ClockSkew: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := placement.New(store.OpenTest(t), placement.Options{AuthorityReady: p.ClockReady, MaxSessions: 32, MaxLease: time.Minute, LockTimeout: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = ledger.SetPool(ctx, "cpu", config.RoleTranscode, 4); err != nil {
		t.Fatal(err)
	}
	target := placement.Target{NodeID: "worker", Boot: "boot"}
	if err = ledger.SetTarget(ctx, target, config.NodeRoles{config.RoleTranscode}, map[config.NodeRole][]string{config.RoleTranscode: {"cpu"}}); err != nil {
		t.Fatal(err)
	}
	request := func(id string) placement.Request {
		return placement.Request{Session: id, Operation: "start", Controller: placement.Controller{ID: "controller", Boot: "boot"}, Target: target, Role: config.RoleTranscode, Claims: []placement.Claim{{Scope: "cpu", Units: 1}}, Lease: 10 * time.Second}
	}
	advance := func(drift time.Duration) {
		f.wall = f.wall.Add(5*time.Second + drift)
		f.mono = f.mono.Add(5 * time.Second)
		f.db = f.db.Add(5 * time.Second)
	}
	sample := func() { p.checkClock(ctx) }
	denied := func(id string) {
		t.Helper()
		if _, err := ledger.Reserve(ctx, request(id)); !errors.Is(err, placement.ErrFenced) {
			t.Fatalf("new authority admitted: %v", err)
		}
		if s := p.ClockStatus(); s.Ready || s.Reason != "clock_skew" {
			t.Fatalf("bad diagnostic: %+v", s)
		}
	}
	denied("unknown")
	sample()
	advance(0)
	sample()
	if !p.ClockReady() {
		t.Fatal("initial synchronized samples did not open admission")
	}
	record, err := ledger.Reserve(ctx, request("existing"))
	if err != nil {
		t.Fatal(err)
	}
	// 9ms per heartbeat remains below step detection but eventually drifts out.
	for range 60 {
		advance(9 * time.Millisecond)
		sample()
	}
	denied("drift")
	if p.Health().ClockSkew < 500*time.Millisecond {
		t.Fatalf("health skew not refreshed: %+v", p.Health())
	}
	if err := ledger.Renew(ctx, record.Fence, record.Deadline.Add(time.Second)); !errors.Is(err, placement.ErrFenced) {
		t.Fatalf("renewal admitted during drift: %v", err)
	}
	replacement := request("existing")
	replacement.ExpectedGeneration = record.Generation
	replacement.Operation = "clock-replacement"
	replacement.Target.NodeID = "other-worker"
	sealed := false
	if _, err := ledger.Supersede(ctx, record.Fence, replacement, func(context.Context, *sql.Tx, placement.Record) error { sealed = true; return nil }); !errors.Is(err, placement.ErrFenced) || sealed {
		t.Fatalf("quarantined supersession mutated authority: %v sealed=%v", err, sealed)
	}
	if existing, err := ledger.Lookup(ctx, record.Session); err != nil || !existing.Authority {
		t.Fatalf("existing authority revoked: %+v %v", existing, err)
	}
	// Step backwards between heartbeats; admission itself must detect it.
	f.wall = f.wall.Add(-2350 * time.Millisecond)
	denied("step")
	if p.ClockStatus().LastStep != -2350*time.Millisecond || !strings.Contains(logs.String(), "local wall clock stepped") {
		t.Fatal("wall step not detected/logged")
	}
	f.wall = f.db
	sample() // correction step cannot immediately restore authority
	if p.ClockReady() {
		t.Fatal("step bypassed hysteresis")
	}
	advance(0)
	sample()
	if p.ClockReady() {
		t.Fatal("one sample bypassed hysteresis")
	}
	advance(0)
	sample()
	if !p.ClockReady() {
		t.Fatalf("automatic recovery failed: %+v", p.ClockStatus())
	}
	if _, err := ledger.Reserve(ctx, request("recovered")); err != nil {
		t.Fatal(err)
	}
	// A slow query also closes the gate even with a zero midpoint offset.
	p.clock.sample(f.wall, f.wall.Add(time.Second), f.mono, f.mono.Add(time.Second), f.wall.Add(time.Second/2), true)
	if p.ClockReady() {
		t.Fatal("RTT uncertainty ignored")
	}
	f.unavailable = true
	sample()
	denied("outage")
	f.unavailable = false
	advance(0)
	sample()
	advance(0)
	sample()
	advance(0)
	sample()
	if !p.ClockReady() {
		t.Fatal("outage recovery failed")
	}
	advance(0)
	advance(0)
	advance(0)
	advance(0)
	denied("stale")
	f.wall = f.wall.AddDate(-500, 0, 0)
	sample()
	advance(0)
	sample()
	advance(0)
	sample()
	denied("extreme-step")
	if p.ClockStatus().Uncertainty <= 0 {
		t.Fatal("extreme clock error overflowed")
	}
	t.Logf("drift, -2.35s step, RTT uncertainty, outage, stale sample and hysteretic recovery verified; existing placement preserved")
}
