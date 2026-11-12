package mdblist

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/state"
)

// Service is supplied explicit state and credential dependencies. Manual sync
// uses a lease and a deadline shorter than the lease; it never advances the
// snapshot after a partial pull.
type Service struct {
	Client Client
	Tokens TokenStore
	State  *state.State
}

type SyncResult struct {
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
}

// Sync imports a viewer's MDBList watch history. It is intended as a one-time
// (or occasional) backfill; ongoing sync is export-only, since MDBList's
// watched endpoint is a snapshot rather than a per-item change feed.
func (s Service) Sync(ctx context.Context, user int64) (SyncResult, error) {
	if s.State == nil {
		return SyncResult{}, errors.New("mdblist: state unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	lease, err := s.Tokens.AcquireLease(ctx, user, time.Now())
	if err != nil {
		return SyncResult{}, err
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer releaseCancel()
		_ = s.Tokens.ReleaseLease(releaseCtx, user, lease)
	}()
	grant, err := s.Tokens.Load(ctx, user)
	if err != nil {
		return SyncResult{}, err
	}
	var sealed, snapshot string
	if err = s.Tokens.DB.QueryRowContext(ctx, `SELECT api_key,snapshot FROM mdblist_accounts WHERE user_id=? AND lease_id=? AND lease_until>?`, user, lease, time.Now().UnixMilli()).Scan(&sealed, &snapshot); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SyncResult{}, ErrBusy
		}
		return SyncResult{}, err
	}
	records, next, skipped, err := s.Client.PullHistory(ctx, user, grant.APIKey, snapshot)
	if err != nil {
		return SyncResult{}, s.exportFailure(ctx, user, lease, err)
	}
	if err = commitLeasedHistory(ctx, s.State, user, sealed, snapshot, next, lease, records); err != nil {
		return SyncResult{}, err
	}
	return SyncResult{Imported: len(records), Skipped: skipped}, nil
}

func commitLeasedHistory(ctx context.Context, st *state.State, user int64, sealed, previous, next, lease string, records []state.ImportRecord) error {
	if user <= 0 || sealed == "" || lease == "" {
		return errors.New("mdblist: invalid import commit")
	}
	for _, r := range records {
		if r.UserID != user {
			return errors.New("mdblist: import viewer mismatch")
		}
	}
	return st.ImportTransaction(ctx, func(tx *sql.Tx) (state.ImportBatch, error) {
		res, err := tx.ExecContext(ctx, `UPDATE mdblist_accounts SET snapshot=? WHERE user_id=? AND api_key=? AND snapshot=? AND lease_id=? AND lease_until>?`, next, user, sealed, previous, lease, time.Now().UnixMilli())
		if err != nil {
			return state.ImportBatch{}, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return state.ImportBatch{}, err
		}
		if n != 1 {
			return state.ImportBatch{}, ErrBusy
		}
		return state.ImportBatch{Records: records}, nil
	})
}
