package clusterplayback

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
)

// Forgetting terminal in-memory state. HistoryCapacity bounds live entries,
// not every playback a boot has ever served: an entry is dropped once it is
// locally quiescent, its session's generation is durably terminal (the ledger
// retired or superseded it and no displaced generation is still charged), and
// it has stayed eligible for forgetGrace. The grace outlives every RPC
// deadline, the coordinator lease and the encoder failure grace, so no
// in-flight request for the generation can still be acting on the entry.
//
// Dropping an entry never re-opens authority: a late bind, resolve, start or
// renewal for a terminal generation is refused by the durable ledger (and by
// nodeauthority, which re-checks placement on every bind). A missing
// preparation falls back to its durable receipt, and a missing execution reads
// as retired everywhere it is consulted. This is the in-memory counterpart of
// PruneRetained, keyed off the same terminal state.
const (
	forgetGrace = 2 * time.Minute
	// forgetChecks bounds the durable lookups one Tick spends on forgetting.
	forgetChecks = 64
)

// executionSlotLocked is the coordinator's admission guard: a session may be
// tracked unless one of its executions is still live, or HistoryCapacity live
// and not-yet-forgotten executions are already tracked.
func (r *Runtime) executionSlotLocked(id string) bool {
	x := r.executions[id]
	return !r.closed && (x != nil && x.retired || x == nil && len(r.executions) < r.o.HistoryCapacity)
}

// generationTerminal reports durable proof that generation gen of session can
// never run or need retirement proof again.
func (r *Runtime) generationTerminal(ctx context.Context, session string, gen int64) bool {
	rec, err := r.o.Placement.Lookup(ctx, session)
	switch {
	case errors.Is(err, placement.ErrFenced): // no row: compacted after retirement
	case err != nil:
		return false
	case rec.Generation < gen, rec.Generation == gen && !rec.Retired:
		return false
	}
	var one int
	err = r.o.DB.QueryRowContext(ctx, `SELECT 1 FROM placement_displaced WHERE session=? AND generation<=? LIMIT 1`, session, gen).Scan(&one)
	return errors.Is(err, sql.ErrNoRows)
}

func closedChannel() chan struct{} { c := make(chan struct{}); close(c); return c }

var alreadyDone = closedChannel()

func channelDone(c <-chan struct{}) bool {
	if c == nil {
		return true
	}
	select {
	case <-c:
		return true
	default:
		return false
	}
}

func (x *execution) forgettable() bool {
	if !x.retired || x.busy || x.reservePending || x.replacement != nil || x.output != nil && !x.outputReleased || !channelDone(x.done) {
		return false
	}
	for _, f := range x.failed {
		if !f.retired {
			return false
		}
	}
	return true
}

func (r *Runtime) sourceForgettable(s *ownedSource) bool {
	if s.binding || s.issuing || s.operations != 0 || r.bytes != nil && r.bytes.Pending(scopeForRoute(s.route)) != 0 {
		return false
	}
	if s.handle.Value() == "" {
		return true // a bind that never completed holds no authority
	}
	return s.sealed && s.retiring && s.life != nil && channelDone(s.life.Done()) && s.life.Err() == nil
}

func (r *Runtime) preparationForgettableLocked(p *preparation) bool {
	if !p.receipt || p.running || p.operations != 0 {
		return false
	}
	for _, s := range r.sources {
		if p.life != nil && s.life == p.life {
			return false
		}
	}
	return true
}

func inputForgettable(v *workerInput) bool {
	return v.input == nil || channelDone(v.input.InputDone())
}

// eligible advances one entry's grace clock and reports whether it is due.
func eligible(at *time.Time, ok bool, now time.Time) bool {
	if !ok {
		*at = time.Time{}
		return false
	}
	if at.IsZero() {
		*at = now.Add(forgetGrace)
		return false
	}
	return !now.Before(*at)
}

// forgetTerminal runs once per Tick with a bounded number of durable checks.
func (r *Runtime) forgetTerminal(ctx context.Context) {
	now := time.Now()
	type due struct {
		session    string
		generation int64
		forget     func() bool // under r.mu: recheck identity and quiescence, delete
		after      func()      // outside r.mu: release resources
		retry      *time.Time
	}
	var list []due
	r.mu.Lock()
	for id, x := range r.executions {
		if len(list) < forgetChecks && eligible(&x.forgetAt, x.forgettable(), now) {
			x, id := x, id
			list = append(list, due{session: id, generation: x.route.Fence.Generation, retry: &x.forgetAt, forget: func() bool {
				if r.executions[id] != x || !x.forgettable() {
					return false
				}
				delete(r.executions, id)
				return true
			}})
		}
	}
	for key, s := range r.sources {
		if len(list) < forgetChecks && eligible(&s.forgetAt, r.sourceForgettable(s), now) {
			key, s := key, s
			list = append(list, due{session: s.route.Fence.Session, generation: s.route.Fence.Generation, retry: &s.forgetAt, forget: func() bool {
				if r.sources[key] != s || !r.sourceForgettable(s) {
					return false
				}
				delete(r.sources, key)
				return true
			}})
		}
	}
	for id, v := range r.inputs {
		if len(list) < forgetChecks && eligible(&v.forgetAt, inputForgettable(v), now) {
			id, v := id, v
			list = append(list, due{session: v.route.Fence.Session, generation: v.route.Fence.Generation, retry: &v.forgetAt, forget: func() bool {
				if r.inputs[id] != v || !inputForgettable(v) {
					return false
				}
				delete(r.inputs, id)
				return true
			}, after: func() {
				v.cancel()
				_ = v.gateway.Close()
				go func() { <-v.done; v.client.Close() }()
			}})
		}
	}
	for id, m := range r.mediaSessions {
		if len(list) < forgetChecks && eligible(&m.forgetAt, true, now) {
			id, m := id, m
			list = append(list, due{session: id, generation: m.route.Fence.Generation, retry: &m.forgetAt, forget: func() bool {
				if r.mediaSessions[id] != m {
					return false
				}
				delete(r.mediaSessions, id)
				return true
			}, after: func() {
				if m.adapter != nil {
					m.adapter.Close()
				}
			}})
		}
	}
	// A preparation's durable receipt is its terminal proof: publication is
	// confirmed by readback before entry.receipt is set.
	for key, p := range r.preparations {
		if eligible(&p.forgetAt, r.preparationForgettableLocked(p), now) {
			delete(r.preparations, key)
		}
	}
	remote := r.remotePlayback
	r.mu.Unlock()
	if remote != nil {
		remote.forgetRetired(now)
	}
	for _, d := range list {
		if ctx.Err() != nil {
			return
		}
		if !r.generationTerminal(ctx, d.session, d.generation) {
			r.mu.Lock()
			*d.retry = time.Now().Add(forgetGrace)
			r.mu.Unlock()
			continue
		}
		r.mu.Lock()
		gone := d.forget()
		r.mu.Unlock()
		if gone && d.after != nil {
			d.after()
		}
	}
	if r.authority != nil {
		r.authority.Forget(ctx, forgetGrace, forgetChecks)
	}
	if r.worker != nil {
		r.worker.Forget(ctx, forgetChecks)
	}
}

// forgetRetired drops frontend preparations whose source-side retirement was
// already observed (receipt or authenticated boot replacement).
func (p *RemotePlayback) forgetRetired(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, e := range p.entries {
		if eligible(&e.forgetAt, e.retired && !e.preparing && e.uses == 0 && e.pins == 0, now) {
			delete(p.entries, id)
		}
	}
}

// workerTerminal is the remotetranscode Worker's durable terminal check.
func (r *Runtime) workerTerminal(ctx context.Context, id remotetranscode.Identity) bool {
	return r.generationTerminal(ctx, id.Session, int64(id.Generation))
}

// preparationSealed reports a durable receipt for a preparation this boot has
// forgotten: a replayed Prepare stays sealed and retirement reads as done.
func (r *Runtime) preparationSealed(ctx context.Context, peer nodewire.Identity, operation string) bool {
	_, err := r.readPreparationReceipt(ctx, r.o.Self, peer, preparationHash(operation))
	return err == nil
}
