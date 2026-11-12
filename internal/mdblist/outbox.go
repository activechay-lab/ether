package mdblist

import (
	"context"
	"database/sql"
	"time"

	"github.com/braedonsaunders/ether/internal/state"
)

// Enqueue records local completions only for linked viewers. Imports do not
// echo back to their source, and an unwatch never deletes remote history.
func Enqueue(ctx context.Context, db *sql.DB, ev state.Event) error {
	if ev.Imported || ev.Account || (ev.Kind != state.EventCompleted && ev.Kind != state.EventScrobble) || ev.UserID <= 0 || ev.At.IsZero() {
		return nil
	}
	if _, err := HistoryPayload(ev.Key, ev.At); err != nil {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockViewer(ctx, db, tx, ev.UserID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO mdblist_outbox(user_id,rating_key,watched_at) SELECT ?,?,? WHERE EXISTS (SELECT 1 FROM mdblist_accounts WHERE user_id=?) AND (SELECT COUNT(*) FROM mdblist_outbox WHERE user_id=?)<10000 ON CONFLICT DO NOTHING`, ev.UserID, int64(ev.Key), ev.At.UTC().Truncate(time.Second).Unix(), ev.UserID, ev.UserID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		_, err = tx.ExecContext(ctx, `UPDATE mdblist_accounts SET last_warning=? WHERE user_id=? AND (SELECT COUNT(*) FROM mdblist_outbox WHERE user_id=?)>=10000`, "queue_full", ev.UserID, ev.UserID)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
