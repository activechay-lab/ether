package subtitles

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("subtitles_auto", 1, `
 CREATE TABLE subtitles_auto_attempts (
 viewer_id INTEGER NOT NULL, rating_key INTEGER NOT NULL,
 token TEXT NOT NULL, retry_at INTEGER NOT NULL, spent INTEGER NOT NULL DEFAULT 0,
 status TEXT NOT NULL, PRIMARY KEY(viewer_id, rating_key));
 CREATE TABLE subtitles_auto_provider_pause (
 provider TEXT PRIMARY KEY, until_at INTEGER NOT NULL);
 `)
	store.RegisterViewerPurge("subtitles_auto", []string{"subtitles_auto_attempts"}, func(ctx context.Context, tx *sql.Tx, id int64) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM subtitles_auto_attempts WHERE viewer_id=?`, id)
		return err
	})
	store.RegisterViewerMerge("subtitles_auto", []string{"subtitles_auto_attempts"}, mergeAutomaticBudget)

}

// AutomaticBudget is the shared-database arbitration boundary for automatic
// subtitle work. It does not schedule playback work or authorize a viewer.
// Callers must establish actual playing, effective preferences and current
// manual/source revisions before using it. Cache reads need no reservation.
//
// The permanent spent bit MUST be committed before any quota-consuming provider
// request. A crash, timeout, source change or ambiguous HTTP outcome never clears
// it. Session, source, language and node IDs deliberately are not budget keys.
// Use the playable episode rating key, never its show/season key.
type AutomaticBudget struct{ db *sql.DB }

func NewAutomaticBudget(db *sql.DB) *AutomaticBudget { return &AutomaticBudget{db: db} }

// AutomaticAttempt is an opaque fencing token for one search attempt. It cannot
// be constructed outside this package. A replacement search invalidates it.
type AutomaticAttempt struct {
	viewer int64
	key    core.RatingKey
	token  string
}

var ErrAutomaticBudgetUnavailable = errors.New("automatic subtitle budget unavailable")

// Begin reserves search work, with a durable six-hour negative/retry boundary.
// Expiry allows another search, never another download after Spend succeeds.
// SQL errors fail closed. Provider pause applies across all viewers and titles.
func (b *AutomaticBudget) Begin(ctx context.Context, viewer int64, key core.RatingKey, now time.Time) (AutomaticAttempt, bool, error) {
	if b == nil || b.db == nil || viewer <= 0 || (key.Kind() != core.KindMovie && key.Kind() != core.KindEpisode) {
		return AutomaticAttempt{}, false, ErrAutomaticBudgetUnavailable
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return AutomaticAttempt{}, false, err
	}
	a := AutomaticAttempt{viewer: viewer, key: key, token: hex.EncodeToString(nonce[:])}
	result, err := b.db.ExecContext(ctx, `INSERT INTO subtitles_auto_attempts(viewer_id,rating_key,token,retry_at,spent,status)
 SELECT ?,?,?,?,0,'searching' WHERE NOT EXISTS
 (SELECT 1 FROM subtitles_auto_provider_pause WHERE provider='opensubtitles' AND until_at>?)
 ON CONFLICT(viewer_id,rating_key) DO UPDATE SET token=excluded.token,retry_at=excluded.retry_at,status='searching'
 WHERE subtitles_auto_attempts.spent=0 AND subtitles_auto_attempts.retry_at<=?`,
		viewer, int64(key), a.token, now.Add(6*time.Hour).UnixMilli(), now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return AutomaticAttempt{}, false, err
	}
	n, err := result.RowsAffected()
	return a, n == 1 && err == nil, err
}

// Spend commits the irreversible reservation. A false result MUST prohibit the
// download. This conditional write also fences expired/replaced search workers.
// Repeated Spend calls with the same token are false (not idempotent permission).
func (b *AutomaticBudget) Spend(ctx context.Context, a AutomaticAttempt, now time.Time) (bool, error) {
	if b == nil || b.db == nil || a.token == "" {
		return false, ErrAutomaticBudgetUnavailable
	}
	result, err := b.db.ExecContext(ctx, `UPDATE subtitles_auto_attempts SET spent=1,status='reserved'
 WHERE viewer_id=? AND rating_key=? AND token=? AND spent=0 AND status='searching' AND retry_at>?
 AND NOT EXISTS (SELECT 1 FROM subtitles_auto_provider_pause WHERE provider='opensubtitles' AND until_at>?)`,
		a.viewer, int64(a.key), a.token, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1 && err == nil, err
}

// Finish stores bounded, secret-free machine status. It never refunds a spent
// reservation. Failure to persist a status cannot turn an uncertain request into
// a retry. A search finishing after replacement cannot change the new worker.
func (b *AutomaticBudget) Finish(ctx context.Context, a AutomaticAttempt, status string) error {
	if b == nil || b.db == nil || a.token == "" {
		return ErrAutomaticBudgetUnavailable
	}
	switch status {
	case "no_match", "cancelled", "quota", "error", "cached", "attached":
	default:
		return errors.New("invalid automatic subtitle status")
	}
	_, err := b.db.ExecContext(ctx, `UPDATE subtitles_auto_attempts SET status=? WHERE viewer_id=? AND rating_key=? AND token=?`, status, a.viewer, int64(a.key), a.token)
	return err
}

// Pause persists the provider's complete Retry-After/quota deadline, without
// shortening it to an HTTP worker's wait budget. A shorter concurrent pause never
// overrides a longer one. Call this before finishing a quota-limited attempt.
func (b *AutomaticBudget) Pause(ctx context.Context, until time.Time) error {
	if b == nil || b.db == nil {
		return ErrAutomaticBudgetUnavailable
	}
	_, err := b.db.ExecContext(ctx, `INSERT INTO subtitles_auto_provider_pause(provider,until_at) VALUES('opensubtitles',?)
 ON CONFLICT(provider) DO UPDATE SET until_at=excluded.until_at
 WHERE subtitles_auto_provider_pause.until_at<excluded.until_at`, until.UnixMilli())
	return err
}

// AutomaticStatus is safe for structured logs and an owner status adapter. No
// credentials, release names, URLs or raw provider responses are stored here.
type AutomaticStatus struct {
	Status  string
	Spent   bool
	RetryAt time.Time
}

func (b *AutomaticBudget) Status(ctx context.Context, viewer int64, key core.RatingKey) (AutomaticStatus, error) {
	if b == nil || b.db == nil {
		return AutomaticStatus{}, ErrAutomaticBudgetUnavailable
	}
	var out AutomaticStatus
	var spent int
	var retry int64
	err := b.db.QueryRowContext(ctx, `SELECT status,spent,retry_at FROM subtitles_auto_attempts WHERE viewer_id=? AND rating_key=?`, viewer, int64(key)).Scan(&out.Status, &spent, &retry)
	out.Spent = spent != 0
	out.RetryAt = time.UnixMilli(retry)
	return out, err
}

// Merge preserves either identity's spent bit and invalidates in-flight searches.
func mergeAutomaticBudget(ctx context.Context, tx *sql.Tx, from, into int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO subtitles_auto_attempts(viewer_id,rating_key,token,retry_at,spent,status)
 SELECT ?,rating_key,'',retry_at,spent,'cancelled' FROM subtitles_auto_attempts WHERE viewer_id=?
 ON CONFLICT(viewer_id,rating_key) DO UPDATE SET token='',status='cancelled',
 spent=CASE WHEN excluded.spent>subtitles_auto_attempts.spent THEN excluded.spent ELSE subtitles_auto_attempts.spent END,
 retry_at=CASE WHEN excluded.retry_at>subtitles_auto_attempts.retry_at THEN excluded.retry_at ELSE subtitles_auto_attempts.retry_at END`, into, from)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM subtitles_auto_attempts WHERE viewer_id=?`, from)
	return err
}
