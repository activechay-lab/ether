package playback

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// Keyframe indexes (playback_index, 70–360 KB each) and probes
// (playback_probe) are written for generations whose resume row never lands:
// a failed or superseded save, a standby alternate that was never swapped in.
// Only a resume row's expiry or a later save of another release removed them,
// so these leaked. The orphan sweep removes a row no playback_resume row names
// with the same (user_id, rating_key, release_id).
//
// Neither table can say how old a row is (playback_index has no timestamp),
// so a row is deleted only once two consecutive sweeps, an hour apart, both
// saw it orphaned. A save racing the sweep, on this node or another one
// sharing the database, has long finished by then. The observations live in
// memory: a restart only delays a deletion by one sweep.

// orphanTables are the tables keyed (user_id, rating_key, release_id) whose
// rows belong to the resume row of that release.
var orphanTables = [...]string{"playback_index", "playback_probe"}

// orphanSweepEvery spaces sweeps: two sweeps, an hour apart, must see a row
// orphaned before it goes.
const orphanSweepEvery = time.Hour

// orphanPage is how many candidate keys one read returns.
const orphanPage = 1000

type orphanRow struct {
	table   string
	user    int64
	key     core.RatingKey
	release string
}

// sweepOrphans deletes index and probe rows that no resume row names and that
// the previous sweep already saw orphaned. Rows of a viewer's title with a
// session on this node (sessions) are kept: a reopen of the release reads
// them. So is the release this process holds as that title's unsaved resume
// (livePoint reads its index). While resume writes are held through a
// database outage the rows they will name are not in the table yet, so the
// sweep waits. It reports the rows deleted.
func (s *resumeStore) sweepOrphans(ctx context.Context, sessions map[resumeKey]bool) (int64, error) {
	if s.held != nil && s.held.Depth() > 0 {
		return 0, nil
	}
	s.orphanMu.Lock()
	defer s.orphanMu.Unlock()
	var found []orphanRow
	for _, table := range orphanTables {
		rows, err := s.orphanCandidates(ctx, table)
		if err != nil {
			return 0, err
		}
		found = append(found, rows...)
	}
	s.freshMu.Lock()
	liveRel := make(map[resumeKey]string, len(s.live))
	for k, l := range s.live {
		liveRel[k] = l.releaseID
	}
	s.freshMu.Unlock()

	prev := s.orphanSeen
	seen := make(map[orphanRow]bool, len(found))
	var due []orphanRow
	for _, r := range found {
		k := resumeKey{r.user, r.key}
		if sessions[k] {
			continue
		}
		if rel, ok := liveRel[k]; ok && rel == r.release {
			continue
		}
		seen[r] = true
		if prev[r] {
			due = append(due, r)
		}
	}
	// Until a due row is handled it stays observed, so a failed delete is
	// retried on the next sweep.
	s.orphanSeen = seen
	var total int64
	for _, r := range due {
		n, err := s.deleteOrphan(ctx, r)
		if err != nil {
			return total, err
		}
		total += n
		delete(seen, r)
	}
	return total, nil
}

// orphanCandidates lists table's rows with no resume row for their release,
// in primary-key order. The scan reads the table's primary-key index and
// probes playback_resume by its primary key; neither reads the large JSON.
func (s *resumeStore) orphanCandidates(ctx context.Context, table string) ([]orphanRow, error) {
	q := `SELECT t.user_id, t.rating_key, t.release_id FROM ` + table + ` t
WHERE (t.user_id, t.rating_key, t.release_id) > (?, ?, ?)
AND NOT EXISTS (SELECT 1 FROM playback_resume r WHERE r.user_id=t.user_id AND r.rating_key=t.rating_key AND r.release_id=t.release_id)
ORDER BY t.user_id, t.rating_key, t.release_id LIMIT ?`
	var (
		out       []orphanRow
		user, key int64 = math.MinInt64, math.MinInt64
		release   string
	)
	for {
		rows, err := s.db.QueryContext(ctx, q, user, key, release, orphanPage)
		if err != nil {
			return nil, err
		}
		n := 0
		for rows.Next() {
			if err := rows.Scan(&user, &key, &release); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, orphanRow{table: table, user: user, key: core.RatingKey(key), release: release})
			n++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if n < orphanPage {
			return out, nil
		}
	}
}

// deleteOrphan deletes one row if its release still has no resume row. The
// transaction takes the title's resume lock first, so on PostgreSQL a resume
// write in flight commits before the existence check runs (SQLite holds the
// database write lock from BEGIN).
func (s *resumeStore) deleteOrphan(ctx context.Context, r orphanRow) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := store.LockKey(ctx, store.DialectOf(s.db), tx, fmt.Sprintf("playback:resume:%d:%d", r.user, r.key)); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM `+r.table+` WHERE user_id=? AND rating_key=? AND release_id=?
AND NOT EXISTS (SELECT 1 FROM playback_resume WHERE user_id=? AND rating_key=? AND release_id=?)`,
		r.user, int64(r.key), r.release, r.user, int64(r.key), r.release)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}
