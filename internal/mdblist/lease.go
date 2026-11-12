package mdblist

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

var ErrBusy = errors.New("mdblist: account is busy")

// AcquireLease reserves a viewer account across nodes for bounded remote work.
// Operations must end before the deadline; every update also checks the lease
// id, since a paused process may wake after losing its reservation.
func (s TokenStore) AcquireLease(ctx context.Context, user int64, now time.Time) (string, error) {
	if user <= 0 || s.DB == nil {
		return "", errors.New("mdblist: account storage unavailable")
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b[:])
	res, err := s.DB.ExecContext(ctx, `UPDATE mdblist_accounts SET lease_id=?,lease_until=? WHERE user_id=? AND lease_until<? AND backoff_until<=?`, id, now.Add(90*time.Second).UnixMilli(), user, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", ErrBusy
	}
	return id, nil
}

func (s TokenStore) ReleaseLease(ctx context.Context, user int64, id string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE mdblist_accounts SET lease_id='',lease_until=0 WHERE user_id=? AND lease_id=?`, user, id)
	return err
}
