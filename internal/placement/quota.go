package placement

import (
	"context"
	"database/sql"
)

// EnsureLedgerQuota seeds or verifies the fleet's durable admission ceiling.
// It never raises an existing policy. Every fresh Reserve checks this same row
// under the placement transaction lock; stale coordinator settings fail closed.
func (s *Service) EnsureLedgerQuota(ctx context.Context) error {
	return s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO placement_policy(id,max_sessions) VALUES(1,?) ON CONFLICT(id) DO NOTHING`, s.options.MaxSessions)
		if err != nil {
			return err
		}
		return s.checkLedgerQuota(ctx, tx)
	})
}
func (s *Service) checkLedgerQuota(ctx context.Context, tx *sql.Tx) error {
	var ceiling int
	err := tx.QueryRowContext(ctx, `SELECT max_sessions FROM placement_policy WHERE id=1`).Scan(&ceiling)
	// Standalone primitive users preserve their pre-policy behavior. Deployments
	// must call EnsureLedgerQuota before admission. Once seeded, no opt-out exists.
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if ceiling != s.options.MaxSessions {
		return ErrQuotaPolicy
	}
	return nil
}

// RaiseLedgerQuota is an explicit monotonic compare-and-swap operation for a
// trusted operator. Updating deployment JSON alone cannot raise the fleet policy.
// Old coordinators keep existing-work renewal/retirement but cannot allocate new
// generations until configured with the newly committed ceiling.
func (s *Service) RaiseLedgerQuota(ctx context.Context, expected int) error {
	if expected < 1 || s.options.MaxSessions <= expected {
		return ErrInvalid
	}
	return s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE placement_policy SET max_sessions=? WHERE id=1 AND max_sessions=?`, s.options.MaxSessions, expected)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrQuotaPolicy
		}
		return nil
	})
}

type QuotaStatus struct {
	Capacity int `json:"capacity"`
	// Live is what the quota bounds; Used adds retained tombstones awaiting
	// compaction, which never refuse admission.
	Live      int  `json:"live"`
	Used      int  `json:"used"`
	Retired   int  `json:"retired"`
	Exhausted bool `json:"exhausted"`
	Matches   bool `json:"matches"`
}

func (s *Service) LedgerStatus(ctx context.Context) (QuotaStatus, error) {
	status := QuotaStatus{}
	err := s.db.QueryRowContext(ctx, `SELECT max_sessions FROM placement_policy WHERE id=1`).Scan(&status.Capacity)
	if err != nil {
		return status, err
	}
	status.Used, status.Retired, err = s.LedgerUsage(ctx)
	status.Live = status.Used - status.Retired
	status.Matches = status.Capacity == s.options.MaxSessions
	status.Exhausted = status.Live >= status.Capacity
	return status, err
}
