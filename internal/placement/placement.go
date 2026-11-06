// Package placement is a durable admission primitive, not a scheduler or an
// execution engine. Only trusted control-plane callers may configure policy or
// submit retirement attestations. Workers must separately enforce fences before
// starting/publishing/stopping work. Lease expiry revokes authority, never proves
// process retirement, and never frees capacity.
package placement

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/store"
)

type Error string

func (e Error) Error() string { return string(e) }

const (
	ErrInvalid     Error = "placement: invalid request"
	ErrFenced      Error = "placement: stale fence or generation"
	ErrConflict    Error = "placement: operation payload conflict"
	ErrCapacity    Error = "placement: capacity exhausted"
	ErrPolicy      Error = "placement: missing or ineligible target/scope policy"
	ErrRetirement  Error = "placement: retirement proof required"
	ErrQuotaPolicy Error = "placement: fleet ledger quota differs; coordinate deployment policy"
	ErrQuota       Error = "placement: session ledger quota exhausted"
)

// UnknownOutcome means a database operation did not produce a confirmed result.
// Reconcile using Lookup and retry the SAME immutable operation. Never assume
// failure means that no reservation/execution exists.
type UnknownOutcome struct{ Err error }

func (e *UnknownOutcome) Error() string { return "placement: unknown outcome: " + e.Err.Error() }
func (e *UnknownOutcome) Unwrap() error { return e.Err }

type Target struct{ NodeID, Boot string }
type Controller struct{ ID, Boot string }
type Fence struct {
	Session    string
	Generation int64
	Controller Controller
	Target     Target
}
type Claim struct {
	Scope string
	Units int64
}
type Request struct {
	Session, Operation string
	ExpectedGeneration int64
	Controller         Controller
	Target             Target
	Role               config.NodeRole
	Claims             []Claim // immutable requested allocation, including after retirement
	Lease              time.Duration
}

// Record is the current durable assignment. Authority is a fresh DB-clock
// observation, not a permit to execute indefinitely. Deadline is unchanged on
// Reserve replay. Retired assignments must never be started, even on replay.
type Record struct {
	InitialDeadline time.Time // immutable Reserve result; replay never inherits a renewal
	Fence
	Operation          string
	Role               config.NodeRole
	Deadline           time.Time
	Retired, Authority bool
	Claims             []Claim // immutable requested allocation, including after retirement
}

// RetirementVerifier validates authenticated exact-fence retirement, or a trusted
// operator attestation that the old execution host has been externally fenced.
// Network silence, node absence, and elapsed deadlines are NOT sufficient proof.
// It runs outside transactions; the full fence is rechecked afterwards.
type RetirementVerifier interface {
	VerifyRetired(context.Context, Fence, string) error
}
type Options struct {
	MaxSessions int // hard ledger bound, including terminal generation floors
	MaxLease    time.Duration
	LockTimeout time.Duration
}
type Service struct {
	db       *sql.DB
	options  Options
	verifier RetirementVerifier
}

func New(db *sql.DB, o Options, verifier RetirementVerifier) (*Service, error) {
	if db == nil || o.MaxSessions < 1 || o.MaxLease < time.Second || o.MaxLease > time.Hour || o.LockTimeout <= 0 || o.LockTimeout > time.Minute {
		return nil, ErrInvalid
	}
	return &Service{db: db, options: o, verifier: verifier}, nil
}
func valid(s string) bool       { return len(s) > 0 && len(s) <= 256 }
func validTarget(t Target) bool { return valid(t.NodeID) && valid(t.Boot) }
func validFence(f Fence) bool {
	return valid(f.Session) && f.Generation > 0 && validTarget(f.Target) && valid(f.Controller.ID) && valid(f.Controller.Boot)
}
func validRole(r config.NodeRole) bool { return config.NodeRoles{r}.Validate() == nil }

// transaction serializes all placement mutations across coordinators. This
// deliberately trades throughput for a simple auditable foundation. Both lock
// wait and SQL work honor a bounded derived context. SQLite uses an actual write
// statement before any reads, including OpenMemory's deferred BEGIN path.
func (s *Service) transaction(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, s.options.LockTimeout)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return &UnknownOutcome{err}
	}
	defer tx.Rollback()
	if err = store.LockKey(ctx, store.DialectOf(s.db), tx, "placement:all"); err == nil && store.DialectOf(s.db) == store.SQLite {
		_, err = tx.ExecContext(ctx, `UPDATE placement_pools SET capacity=capacity WHERE scope=''`)
	}
	if err == nil {
		err = fn(ctx, tx)
	}
	if err != nil {
		var business Error
		if errors.As(err, &business) {
			return err
		}
		return &UnknownOutcome{err}
	}
	if err = tx.Commit(); err != nil {
		return &UnknownOutcome{err}
	}
	return nil
}
func (s *Service) now(ctx context.Context, q store.Querier) (int64, error) {
	query := `SELECT CAST((julianday('now')-2440587.5)*86400000 AS INTEGER)`
	if store.DialectOf(s.db) == store.Postgres {
		query = `SELECT CAST(FLOOR(EXTRACT(EPOCH FROM clock_timestamp())*1000) AS BIGINT)`
	}
	var n int64
	err := q.QueryRowContext(ctx, query).Scan(&n)
	return n, err
}

// SetPool persists a role-specific resource budget. Zero closes admission; absent
// budgets fail closed. Use the same scope for a physical resource shared by
// multiple nodes. Reducing capacity below outstanding claims is rejected.
func (s *Service) SetPool(ctx context.Context, scope string, role config.NodeRole, capacity int64) error {
	if !valid(scope) || !validRole(role) || capacity < 0 {
		return ErrInvalid
	}
	return s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var used int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(units),0) FROM (SELECT scope,units FROM placement_claims UNION ALL SELECT scope,units FROM placement_displaced_claims) charged WHERE scope=?`, scope).Scan(&used); err != nil {
			return err
		}
		if used > capacity {
			return ErrCapacity
		}
		var old string
		err := tx.QueryRowContext(ctx, `SELECT role FROM placement_pools WHERE scope=?`, scope).Scan(&old)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if used > 0 && old != string(role) {
			return ErrPolicy
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO placement_pools(scope,role,capacity) VALUES(?,?,?) ON CONFLICT(scope) DO UPDATE SET role=excluded.role,capacity=excluded.capacity`, scope, string(role), capacity)
		return err
	})
}

// SetTarget persists the currently admitted boot and explicit enabled roles.
// It never releases reservations from any boot. An empty role list closes new
// admission (unlike the config default, explicit policy cannot imply unlimited).
// Only the authenticated membership/control-plane reconciler may call this;
// worker startup must not blindly overwrite another live incarnation.
func (s *Service) SetTarget(ctx context.Context, target Target, roles config.NodeRoles, scopes map[config.NodeRole][]string) error {
	if !validTarget(target) || roles.Validate() != nil {
		return ErrInvalid
	}
	for _, role := range roles {
		names := scopes[role]
		if len(names) == 0 || len(names) > 16 {
			return ErrInvalid
		}
		seen := map[string]bool{}
		for _, name := range names {
			if !valid(name) || seen[name] {
				return ErrInvalid
			}
			seen[name] = true
		}
	}
	policy := make(map[config.NodeRole][]string, len(roles))
	for _, role := range roles {
		policy[role] = append([]string(nil), scopes[role]...)
	}
	data, _ := json.Marshal(roles)
	scopeData, _ := json.Marshal(policy)
	return s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var removed int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM placement_removed_targets WHERE node=? AND boot=?`, target.NodeID, target.Boot).Scan(&removed); err != nil {
			return err
		}
		if removed != 0 {
			return ErrFenced
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO placement_targets(node,boot,roles,scopes) VALUES(?,?,?,?) ON CONFLICT(node) DO UPDATE SET boot=excluded.boot,roles=excluded.roles,scopes=excluded.scopes`, target.NodeID, target.Boot, string(data), string(scopeData))
		return err
	})
}

func normalize(r Request, max time.Duration) (Request, string, error) {
	if !valid(r.Session) || !valid(r.Operation) || !valid(r.Controller.ID) || !valid(r.Controller.Boot) || !validTarget(r.Target) || !validRole(r.Role) || r.ExpectedGeneration < 0 || r.ExpectedGeneration == math.MaxInt64 || r.Lease < time.Second || r.Lease > max || len(r.Claims) == 0 || len(r.Claims) > 16 {
		return r, "", ErrInvalid
	}
	r.Claims = append([]Claim(nil), r.Claims...)
	sort.Slice(r.Claims, func(i, j int) bool { return r.Claims[i].Scope < r.Claims[j].Scope })
	for i, c := range r.Claims {
		if !valid(c.Scope) || c.Units <= 0 || (i > 0 && r.Claims[i-1].Scope == c.Scope) {
			return r, "", ErrInvalid
		}
	}
	data, _ := json.Marshal(r)
	sum := sha256.Sum256(data)
	return r, hex.EncodeToString(sum[:]), nil
}
func read(ctx context.Context, q store.Querier, session string) (Record, string, error) {
	var r Record
	var hash, claimsJSON string
	var deadline, initial int64
	var retired int
	err := q.QueryRowContext(ctx, `SELECT session,generation,operation,fingerprint,controller,controller_boot,node,boot,role,claims,initial_deadline,deadline,retired FROM placement_sessions WHERE session=?`, session).Scan(&r.Session, &r.Generation, &r.Operation, &hash, &r.Controller.ID, &r.Controller.Boot, &r.Target.NodeID, &r.Target.Boot, &r.Role, &claimsJSON, &initial, &deadline, &retired)
	if err != nil {
		return r, "", err
	}
	r.Deadline = time.UnixMilli(deadline)
	r.InitialDeadline = time.UnixMilli(initial)
	r.Retired = retired != 0
	if err = json.Unmarshal([]byte(claimsJSON), &r.Claims); err != nil {
		return r, "", err
	}
	return r, hash, nil
}

// Lookup reconciles unknown outcomes. Its snapshot and authority observation are
// serialized with mutations, so a mixed old fence/new claims record is impossible.
func (s *Service) Lookup(ctx context.Context, session string) (Record, error) {
	var r Record
	if !valid(session) {
		return r, ErrInvalid
	}
	err := s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		r, _, err = read(ctx, tx, session)
		if err == sql.ErrNoRows {
			return ErrFenced
		}
		if err != nil {
			return err
		}
		now, err := s.now(ctx, tx)
		r.Authority = !r.Retired && r.Deadline.UnixMilli() > now
		return err
	})
	return r, err
}

// Reserve atomically admits all scopes or none. ExpectedGeneration=0 creates a
// new logical session; replacement requires the current retired generation.
// Idempotency keys are scoped to a session generation transition. Old replay is
// fenced by ExpectedGeneration after a replacement, including after compaction.
func (s *Service) Reserve(ctx context.Context, request Request) (Record, error) {
	var out Record
	r, hash, err := normalize(request, s.options.MaxLease)
	if err != nil {
		return out, err
	}
	err = s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return s.reserveInTx(ctx, tx, r, hash, &out)
	})
	return out, err
}

func (s *Service) reserveInTx(ctx context.Context, tx *sql.Tx, r Request, hash string, out *Record) error {
	old, oldHash, e := read(ctx, tx, r.Session)
	exists := e == nil
	if e != nil && e != sql.ErrNoRows {
		return e
	}
	now, e := s.now(ctx, tx)
	if e != nil {
		return e
	}
	if exists && old.Operation == r.Operation {
		if oldHash != hash {
			return ErrConflict
		}
		*out = old
		out.Deadline = old.InitialDeadline
		out.Authority = !old.Retired && out.Deadline.UnixMilli() > now
		return nil
	}
	if e := s.checkLedgerQuota(ctx, tx); e != nil {
		return e
	}
	if (!exists && r.ExpectedGeneration != 0) || (exists && old.Generation != r.ExpectedGeneration) {
		return ErrFenced
	}
	if exists && !old.Retired {
		return ErrRetirement
	}
	if !exists {
		var n int
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM placement_sessions`).Scan(&n); e != nil {
			return e
		}
		if n >= s.options.MaxSessions {
			return ErrQuota
		}
	}
	var boot, rolesJSON, scopesJSON string
	e = tx.QueryRowContext(ctx, `SELECT boot,roles,scopes FROM placement_targets WHERE node=?`, r.Target.NodeID).Scan(&boot, &rolesJSON, &scopesJSON)
	if e == sql.ErrNoRows {
		return ErrPolicy
	}
	if e != nil {
		return e
	}
	var roles config.NodeRoles
	if boot != r.Target.Boot || json.Unmarshal([]byte(rolesJSON), &roles) != nil || len(roles) == 0 || roles.Validate() != nil || !roles.Has(r.Role) {
		return ErrPolicy
	}
	var scopes map[config.NodeRole][]string
	if json.Unmarshal([]byte(scopesJSON), &scopes) != nil || len(scopes[r.Role]) != len(r.Claims) {
		return ErrPolicy
	}
	required := map[string]bool{}
	for _, name := range scopes[r.Role] {
		required[name] = true
	}
	for _, c := range r.Claims {
		if !required[c.Scope] {
			return ErrPolicy
		}
	}
	for _, c := range r.Claims {
		var role string
		var capacity, used int64
		e = tx.QueryRowContext(ctx, `SELECT role,capacity FROM placement_pools WHERE scope=?`, c.Scope).Scan(&role, &capacity)
		if e == sql.ErrNoRows {
			return ErrPolicy
		}
		if e != nil {
			return e
		}
		if role != string(r.Role) {
			return ErrPolicy
		}
		if e = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(units),0) FROM (SELECT scope,units FROM placement_claims UNION ALL SELECT scope,units FROM placement_displaced_claims) charged WHERE scope=?`, c.Scope).Scan(&used); e != nil {
			return e
		}
		if c.Units > capacity || used > capacity-c.Units {
			return ErrCapacity
		}
	}
	claimsData, _ := json.Marshal(r.Claims)
	generation := r.ExpectedGeneration + 1
	deadline := now + r.Lease.Milliseconds()
	_, e = tx.ExecContext(ctx, `INSERT INTO placement_sessions(session,generation,operation,fingerprint,controller,controller_boot,node,boot,role,claims,initial_deadline,deadline,retired) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,0) ON CONFLICT(session) DO UPDATE SET generation=excluded.generation,operation=excluded.operation,fingerprint=excluded.fingerprint,controller=excluded.controller,controller_boot=excluded.controller_boot,node=excluded.node,boot=excluded.boot,role=excluded.role,claims=excluded.claims,initial_deadline=excluded.initial_deadline,deadline=excluded.deadline,retired=0`, r.Session, generation, r.Operation, hash, r.Controller.ID, r.Controller.Boot, r.Target.NodeID, r.Target.Boot, string(r.Role), string(claimsData), deadline, deadline)
	if e != nil {
		return e
	}
	for _, c := range r.Claims {
		if _, e = tx.ExecContext(ctx, `INSERT INTO placement_claims(session,scope,units) VALUES(?,?,?)`, r.Session, c.Scope, c.Units); e != nil {
			return e
		}
	}
	*out = Record{Fence: Fence{r.Session, generation, r.Controller, r.Target}, Operation: r.Operation, Role: r.Role, Deadline: time.UnixMilli(deadline), InitialDeadline: time.UnixMilli(deadline), Authority: true, Claims: r.Claims}
	return nil
}

// Renew uses an absolute deadline, so retries cannot extend the requested lease.
// It cannot revive expired authority. A stale renewal never touches replacements.
func (s *Service) Renew(ctx context.Context, f Fence, deadline time.Time) error {
	if !validFence(f) {
		return ErrInvalid
	}
	return s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		r, _, err := read(ctx, tx, f.Session)
		if err == sql.ErrNoRows {
			return ErrFenced
		}
		if err != nil {
			return err
		}
		if r.Fence != f || r.Retired {
			return ErrFenced
		}
		now, err := s.now(ctx, tx)
		if err != nil {
			return err
		}
		if r.Deadline.UnixMilli() <= now {
			return ErrFenced
		}
		next := deadline.UnixMilli()
		if next <= now || next-now > s.options.MaxLease.Milliseconds() {
			return ErrInvalid
		}
		if next <= r.Deadline.UnixMilli() {
			return nil
		}
		_, err = tx.ExecContext(ctx, `UPDATE placement_sessions SET deadline=? WHERE session=?`, next, f.Session)
		return err
	})
}

// Retire is the ONLY capacity release operation, including after timeout or
// permanent node loss. Proof verification runs before taking the component lock;
// an exact-fence recheck prevents stale proof from releasing a replacement.
func (s *Service) Retire(ctx context.Context, f Fence, evidence string) error {
	if !validFence(f) || !valid(evidence) {
		return ErrInvalid
	}
	if s.verifier == nil {
		return ErrRetirement
	}
	if err := s.verifier.VerifyRetired(ctx, f, evidence); err != nil {
		return fmt.Errorf("%w: %v", ErrRetirement, err)
	}
	return s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		r, _, err := read(ctx, tx, f.Session)
		if err == sql.ErrNoRows {
			return ErrFenced
		}
		if err != nil {
			return err
		}
		if r.Fence != f {
			return ErrFenced
		}
		return retireRecord(ctx, tx, r)
	})
}

// retireRecord is called only after trusted verification outside the transaction
// and a full-fence recheck under placement:all. Receipt publication uses the same
// release operation; ordinary Retire retains its existing verifier semantics.
func retireRecord(ctx context.Context, tx *sql.Tx, r Record) error {
	if r.Retired {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM placement_claims WHERE session=?`, r.Session); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE placement_sessions SET retired=1 WHERE session=?`, r.Session)
	return err
}

// LedgerUsage exposes quota pressure. Retired rows ARE compact tombstones: claims
// are removed, but the fixed-size fence, generation and operation digest survive
// forever. There is intentionally no TTL deletion API: discarding a floor would
// allow an arbitrarily delayed generation-zero replay to resurrect old work.
// Raise MaxSessions consistently across coordinators or archive/replace the whole
// cluster namespace under externally established quiescence when quota is reached.
func (s *Service) LedgerUsage(ctx context.Context) (used, retired int, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(retired),0) FROM placement_sessions`).Scan(&used, &retired)
	return
}
