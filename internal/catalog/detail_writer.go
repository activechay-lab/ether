package catalog

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Detail fills arrive in clumps (WarmDetails keeps eight fetches in flight,
// a Home build hydrates a page of titles), and a transaction per title
// rewrote the same shared index pages over and over: on the lab catalogue
// each page a fill touched was written about five times. Detail writes are
// group-committed: one that finds no flush running commits at once (no
// added latency), and those that arrive while a flush runs commit together
// as soon as it ends, up to detailBatchMax a transaction. A failed batch is
// retried title by title, so one bad title fails only its own write.
const detailBatchMax = 32

type detailWrites struct {
	mu       sync.Mutex
	pending  []detailWrite
	flushing bool
}

type detailWrite struct {
	item upsertItem
	// extra rides in the same transaction when it can (the recommendations
	// a detail fetch carried); a failure of extra never fails item.
	extra *detailExtra
	done  chan error
}

// detailExtra is more writes for a detail write's transaction.
type detailExtra struct {
	items []upsertItem
	after func(context.Context, *sql.Tx) error
}

// writeDetail stores one fetched title, batched with concurrent detail
// writes. It returns once the title's transaction committed (or failed).
func (c *Catalog) writeDetail(ctx context.Context, t core.Title, o upsertOpts, extra ...*detailExtra) error {
	w := detailWrite{item: upsertItem{t: t, o: o}, done: make(chan error, 1)}
	if len(extra) > 0 {
		w.extra = extra[0]
	}
	dw := &c.detailWrites
	dw.mu.Lock()
	dw.pending = append(dw.pending, w)
	start := !dw.flushing
	dw.flushing = true
	dw.mu.Unlock()
	if start {
		go c.flushDetailsLoop()
	}
	select {
	case err := <-w.done:
		return err
	case <-ctx.Done():
		// The write goes ahead; the caller stops waiting for it.
		return ctx.Err()
	}
}

func (c *Catalog) flushDetailsLoop() {
	dw := &c.detailWrites
	for {
		dw.mu.Lock()
		n := min(len(dw.pending), detailBatchMax)
		if n == 0 {
			dw.flushing = false
			dw.mu.Unlock()
			return
		}
		batch := append([]detailWrite(nil), dw.pending[:n]...)
		dw.pending = append(dw.pending[:0], dw.pending[n:]...)
		dw.mu.Unlock()
		c.flushDetails(batch)
	}
}

func (c *Catalog) flushDetails(batch []detailWrite) {
	// Detached from any one caller: a cancelled request must not fail the
	// others' writes. Bounded like a background refresh.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	write := func(ws []detailWrite, withExtra bool) error {
		var items []upsertItem
		var after []func(context.Context, *sql.Tx) error
		for _, w := range ws {
			items = append(items, w.item)
		}
		if withExtra {
			for _, w := range ws {
				if w.extra != nil {
					items = append(items, w.extra.items...)
					if w.extra.after != nil {
						after = append(after, w.extra.after)
					}
				}
			}
		}
		return c.upsertItems(ctx, items, after...)
	}
	if err := write(batch, true); err == nil {
		for _, w := range batch {
			w.done <- nil
		}
		return
	}
	// One title at a time; extras are a bonus that must not fail the title.
	for _, w := range batch {
		err := write([]detailWrite{w}, true)
		if err != nil && w.extra != nil {
			err = write([]detailWrite{w}, false)
		}
		w.done <- err
	}
}
