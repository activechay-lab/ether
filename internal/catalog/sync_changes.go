package catalog

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// TMDB's /movie/changes and /tv/changes list the ids edited in a date
// range of at most 14 days. The sync refreshes the stored titles among
// them, and only those: a title TMDB did not edit is never refetched or
// rewritten for its metadata. A title with full details gets a full
// detail fetch; a summary row gets the lean fetch. Both write through
// upsertMany, which writes nothing when nothing changed.

// changesSource is the /changes endpoint (*tmdb.Client).
type changesSource interface {
	Changes(ctx context.Context, tv bool, start, end time.Time, page int) (*tmdb.Paged[tmdb.ChangedID], error)
}

// changesMaxPages bounds one window's paging (TMDB lists about 8,000
// movie ids a day, 100 a page).
const changesMaxPages = 1000

// syncChanges refreshes stored titles TMDB changed since the last pass.
// The first pass only records today: the mirror was just filled.
func (c *Catalog) syncChanges(ctx context.Context) error {
	src, ok := c.tmdb().(changesSource)
	if !ok {
		return nil
	}
	today := c.now().UTC().Truncate(24 * time.Hour)
	since, err := time.Parse("2006-01-02", c.getMeta(ctx, "changes_since"))
	if err != nil || since.After(today) {
		return c.setMeta(ctx, "changes_since", today.Format("2006-01-02"))
	}
	c.setProgress(func(p *SyncProgress) { p.Phase = "changes" })
	for _, tv := range []bool{false, true} {
		ids, err := c.changedIDs(ctx, src, tv, since, today)
		if err != nil {
			return err
		}
		if err := c.refreshChanged(ctx, tv, ids); err != nil {
			return err
		}
	}
	// The start day is read again next time: its later edits are not
	// missed, at the cost of re-reading one day's ids.
	return c.setMeta(ctx, "changes_since", today.Format("2006-01-02"))
}

// changedIDs pages /changes over [since, today] in windows of at most 14
// days and returns the distinct ids.
func (c *Catalog) changedIDs(ctx context.Context, src changesSource, tv bool, since, today time.Time) ([]int64, error) {
	seen := map[int64]bool{}
	var ids []int64
	span := int(tmdb.ChangesMaxWindow/(24*time.Hour)) - 1
	for start := since; !start.After(today); start = start.AddDate(0, 0, span+1) {
		end := start.AddDate(0, 0, span)
		if end.After(today) {
			end = today
		}
		for page := 1; page <= changesMaxPages; page++ {
			res, err := src.Changes(ctx, tv, start, end, page)
			if err != nil {
				return nil, fmt.Errorf("catalog: TMDB changes tv=%t %s..%s page %d: %w", tv, start.Format("2006-01-02"), end.Format("2006-01-02"), page, err)
			}
			for _, ch := range res.Results {
				if ch.ID > 0 && !seen[ch.ID] {
					seen[ch.ID] = true
					ids = append(ids, ch.ID)
				}
			}
			if page >= res.TotalPages || len(res.Results) == 0 {
				break
			}
		}
	}
	return ids, nil
}

// refreshChanged refetches the changed ids the mirror holds.
func (c *Catalog) refreshChanged(ctx context.Context, tv bool, ids []int64) error {
	var detailed []int64
	var lean []uint64
	for start := 0; start < len(ids); start += 500 {
		chunk := ids[start:min(start+500, len(ids))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = int64(titleKey(tv, id))
		}
		rows, err := c.db.QueryContext(ctx, `SELECT key, detail FROM catalog_titles WHERE key IN (`+placeholders(len(chunk))+`)`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var key int64
			var detail int
			if err := rows.Scan(&key, &detail); err != nil {
				rows.Close()
				return err
			}
			id := core.RatingKey(key).TMDB()
			if detail != 0 {
				detailed = append(detailed, id)
			} else {
				lean = append(lean, packFill(0, id))
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	c.log.Info("catalog: TMDB changes", "tv", tv, "changed", len(ids), "stored_detailed", len(detailed), "stored_summary", len(lean))
	if _, err := c.fillByID(ctx, tv, lean); err != nil {
		return err
	}
	return c.refreshDetails(ctx, tv, detailed)
}

// refreshDetails refetches full details, fillWorkers at a time. A title
// gone from TMDB keeps its stored row.
func (c *Catalog) refreshDetails(ctx context.Context, tv bool, ids []int64) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int64)
	var mu sync.Mutex
	var firstErr error
	failures := 0
	var wg sync.WaitGroup
	for range fillWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				var err error
				if tv {
					err = c.fetchShowDetails(ctx, id)
				} else {
					err = c.fetchMovieDetails(ctx, id)
				}
				if err == nil || errors.Is(err, core.ErrNotFound) || ctx.Err() != nil {
					continue
				}
				mu.Lock()
				failures++
				if firstErr == nil {
					firstErr = err
				}
				if failures >= 20 || errors.Is(err, tmdb.ErrUnavailable) {
					cancel()
				}
				mu.Unlock()
			}
		}()
	}
	for _, id := range ids {
		select {
		case jobs <- id:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	if failures >= 20 || errors.Is(firstErr, tmdb.ErrUnavailable) {
		return fmt.Errorf("catalog: refresh changed details: %w", firstErr)
	}
	return nil
}
