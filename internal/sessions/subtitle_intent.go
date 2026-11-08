package sessions

import (
	"context"
	"database/sql"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("sessions_subtitle_intent", 1, `CREATE TABLE sessions_subtitle_intent(viewer_id INTEGER NOT NULL,rating_key INTEGER NOT NULL,revision INTEGER NOT NULL,manual INTEGER NOT NULL,PRIMARY KEY(viewer_id,rating_key));`)
	store.RegisterViewerTable("sessions_subtitle_intent", "sessions_subtitle_intent", "viewer_id", store.ViewerMergeMove, "rating_key")
}

// SubtitleIntent is shared across a viewer's devices, preserving Plex's existing
// per-title manual selection scope. Device-specific choices may conservatively
// suppress automation on another device; they never grant extra download quota.
type SubtitleIntent struct {
	Revision int64
	Manual   bool
}

// SubtitleIntents fences manual changes against publication. The broker and
// adapters must authorize the title and validate selectors BEFORE recording an
// intent. The fence does not replace that authorization or store track choices.
type SubtitleIntents struct{ db *sql.DB }

func NewSubtitleIntents(db *sql.DB) *SubtitleIntents { return &SubtitleIntents{db: db} }

func (s *SubtitleIntents) Record(ctx context.Context, viewer int64, key core.RatingKey, manual bool) error {
	if s == nil || s.db == nil || viewer <= 0 || !key.Valid() {
		return core.ErrNotAvailable
	}
	value := 0
	if manual {
		value = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions_subtitle_intent(viewer_id,rating_key,revision,manual) VALUES(?,?,1,?) ON CONFLICT(viewer_id,rating_key) DO UPDATE SET revision=sessions_subtitle_intent.revision+1,manual=excluded.manual`, viewer, int64(key), value)
	return err
}

func (s *SubtitleIntents) Snapshot(ctx context.Context, viewer int64, key core.RatingKey) (SubtitleIntent, error) {
	if s == nil || s.db == nil || viewer <= 0 || !key.Valid() {
		return SubtitleIntent{}, core.ErrNotAvailable
	}
	// Create a stable row so an absent intent is also lockable across nodes.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO sessions_subtitle_intent(viewer_id,rating_key,revision,manual) VALUES(?,?,0,0) ON CONFLICT(viewer_id,rating_key) DO NOTHING`, viewer, int64(key)); err != nil {
		return SubtitleIntent{}, err
	}
	var out SubtitleIntent
	var manual int
	err := s.db.QueryRowContext(ctx, `SELECT revision,manual FROM sessions_subtitle_intent WHERE viewer_id=? AND rating_key=?`, viewer, int64(key)).Scan(&out.Revision, &manual)
	out.Manual = manual != 0
	return out, err
}

// Publish runs a short in-memory source attachment while holding the database
// row lock. No provider/network call or other database query belongs in fn.
// Manual writers wait until publication finishes; older snapshots fail closed.
func (s *SubtitleIntents) Publish(ctx context.Context, viewer int64, key core.RatingKey, expected SubtitleIntent, fn func() error) error {
	if s == nil || s.db == nil || fn == nil || expected.Manual {
		return core.ErrNotAvailable
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// UPDATE takes a row lock on PostgreSQL and the SQLite write lock. Unlike a
	// deferred SELECT this arbitrates manual intent even with concurrent writers.
	result, err := tx.ExecContext(ctx, `UPDATE sessions_subtitle_intent SET revision=revision WHERE viewer_id=? AND rating_key=? AND revision=? AND manual=0`, viewer, int64(key), expected.Revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return core.ErrSourceChanged
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = fn(); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordAuthorizedSubtitleIntent is for an adapter that has already authorized
// the viewer/title and validated the selector. This is deliberately not an HTTP
// endpoint. Missing DB keeps legacy fixtures working; automation fails closed
// without its store. Reset-default passes manual=false; explicit off passes true.
func (b *Broker) RecordAuthorizedSubtitleIntent(ctx context.Context, viewer int64, key core.RatingKey, manual bool) error {
	if b == nil || b.deps.DB == nil {
		return nil
	}
	return NewSubtitleIntents(b.deps.DB).Record(ctx, viewer, key, manual)
}
