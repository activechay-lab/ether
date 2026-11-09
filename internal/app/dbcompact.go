package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

const (
	autoCompactMinBytes      = 128 << 20
	autoCompactSmallMinBytes = 32 << 20
	autoCompactSmallMinDenom = 5 // >= 20% of database size
)

// databaseCompaction coordinates one-time VACUUM conversion of a pre-incremental
// SQLite database so daily maintenance returns free space from then on.
type databaseCompaction struct {
	mu      sync.Mutex
	running bool
	started time.Time
	last    *compactResult
}

type compactResult struct {
	Finished    time.Time `json:"finished"`
	TookSeconds float64   `json:"tookSeconds"`
	BeforeBytes int64     `json:"beforeBytes"`
	AfterBytes  int64     `json:"afterBytes"`
	Error       string    `json:"error,omitempty"`
}

// compactStatus is GET/POST /ether/api/status/database/compact.
type compactStatus struct {
	store.CompactPlan
	Running bool           `json:"running"`
	Started *time.Time     `json:"started,omitempty"`
	Last    *compactResult `json:"last,omitempty"`
}

var errCompactPlaying = errors.New("stop playback first: compacting pauses database writes while it runs")

func (a *App) compactionStatus(ctx context.Context) (any, error) {
	if a.db == nil {
		return nil, errors.New("database is not available")
	}
	plan, err := store.PlanCompact(ctx, a.db)
	if err != nil {
		return nil, err
	}
	c := &a.compaction
	c.mu.Lock()
	defer c.mu.Unlock()
	st := compactStatus{CompactPlan: plan, Running: c.running, Last: c.last}
	if c.running {
		started := c.started
		st.Started = &started
	}
	if plan.Ready && a.playback != nil && len(a.playback.Sessions()) > 0 {
		st.Ready, st.Blocked = false, "playing"
	}
	return st, nil
}

// startCompaction starts the compaction in the background and answers with
// its status at once; the console polls GET until it finishes.
func (a *App) startCompaction(ctx context.Context) (any, error) {
	v, err := a.compactionStatus(ctx)
	if err != nil {
		return nil, err
	}
	st := v.(compactStatus)
	c := &a.compaction
	c.mu.Lock()
	switch {
	case c.running:
		c.mu.Unlock()
		return st, nil
	case st.Blocked == "playing":
		c.mu.Unlock()
		return nil, errCompactPlaying
	case !st.Ready:
		c.mu.Unlock()
		return nil, store.ErrCompactBlocked
	}
	c.running, c.started = true, time.Now()
	c.mu.Unlock()
	a.publishDashboard()
	go func() {
		res := &compactResult{BeforeBytes: st.DatabaseBytes}
		after, err := store.Compact(context.WithoutCancel(ctx), a.db)
		res.Finished, res.TookSeconds, res.AfterBytes = time.Now(), time.Since(c.started).Seconds(), after.DatabaseBytes
		if err != nil {
			res.Error = err.Error()
			a.log.Warn("database compaction failed", "err", err)
		} else {
			a.log.Info("database compacted", "took", time.Since(c.started).Round(time.Second), "before", st.DatabaseBytes, "after", after.DatabaseBytes)
		}
		c.mu.Lock()
		c.running, c.last = false, res
		c.mu.Unlock()
		a.publishDashboard()
	}()
	return a.compactionStatus(ctx)
}

// maybeAutoCompact runs the one-time auto_vacuum=INCREMENTAL conversion during
// idle maintenance when a pre-incremental SQLite database has accumulated
// substantial free pages (for example after the 0.3.6 tier migration or 0.3.7
// hidden-table cleanup). Once converted, CompactPlan.Incremental is true and
// daily ReleaseFreePages reclaims space in short steps without full VACUUM.
func (a *App) maybeAutoCompact(ctx context.Context) {
	if a.db == nil || store.DialectOf(a.db) != store.SQLite {
		return
	}
	if a.playback != nil && len(a.playback.Sessions()) > 0 {
		return
	}
	plan, err := store.PlanCompact(ctx, a.db)
	if err != nil || plan.Incremental || !plan.Ready {
		return
	}
	worthIt := plan.ReclaimableBytes >= autoCompactMinBytes ||
		(plan.ReclaimableBytes >= autoCompactSmallMinBytes && plan.ReclaimableBytes*autoCompactSmallMinDenom >= plan.DatabaseBytes)
	if !worthIt {
		return
	}
	c := &a.compaction
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	c.running, c.started = true, time.Now()
	started := c.started
	c.mu.Unlock()

	res := &compactResult{BeforeBytes: plan.DatabaseBytes}
	after, err := store.Compact(ctx, a.db)
	res.Finished, res.TookSeconds, res.AfterBytes = time.Now(), time.Since(started).Seconds(), after.DatabaseBytes
	if err != nil {
		res.Error = err.Error()
		if a.log != nil {
			a.log.Warn("automatic database compaction failed", "err", err)
		}
	} else if a.log != nil {
		a.log.Info("database auto-compacted", "took", time.Since(started).Round(time.Second), "before", plan.DatabaseBytes, "after", after.DatabaseBytes)
	}
	c.mu.Lock()
	c.running, c.last = false, res
	c.mu.Unlock()
}
