package mdblist

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("mdblist", 1, `CREATE TABLE mdblist_accounts (
 user_id INTEGER PRIMARY KEY,
 api_key TEXT NOT NULL,
 snapshot TEXT NOT NULL DEFAULT '',
 lease_id TEXT NOT NULL DEFAULT '',
 lease_until INTEGER NOT NULL DEFAULT 0,
 backoff_until INTEGER NOT NULL DEFAULT 0,
 next_post INTEGER NOT NULL DEFAULT 0,
 last_warning TEXT NOT NULL DEFAULT ''
 );
 CREATE TABLE mdblist_post_pacing (
 id INTEGER PRIMARY KEY,
 next_post INTEGER NOT NULL DEFAULT 0
 );
 INSERT INTO mdblist_post_pacing(id,next_post) VALUES(1,0);
 CREATE TABLE mdblist_outbox (
 user_id INTEGER NOT NULL,
 rating_key INTEGER NOT NULL,
 watched_at INTEGER NOT NULL,
 PRIMARY KEY(user_id,rating_key,watched_at),
 FOREIGN KEY(user_id) REFERENCES mdblist_accounts(user_id) ON DELETE CASCADE
 );`)
}

// TokenStore keeps one API key per viewer, sealed and bound to its row. A
// working field sealer is mandatory: keys are never stored in cleartext,
// including during the legacy credential upgrade.
type TokenStore struct {
	DB     *sql.DB
	Sealer config.FieldSealer
}

// Grant is one viewer's MDBList credential together with its lease state.
type Grant struct {
	APIKey string
}

func validateKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("mdblist: API key required")
	}
	if len(key) > 512 || strings.ContainsAny(key, " \t\r\n") {
		return errors.New("mdblist: invalid API key")
	}
	return nil
}

func (s TokenStore) seal(key string, user int64) (string, error) {
	v, err := s.Sealer.SealField(key, "mdblist_accounts", "api_key", config.FieldContext(user))
	if err != nil {
		return "", err
	}
	if !config.IsSealed(v) {
		return "", errors.New("mdblist: credential sealing is not enabled")
	}
	return v, nil
}

// Save stores or replaces a viewer's API key. The key is bound to that
// viewer, so a value copied to another row fails to open.
func (s TokenStore) Save(ctx context.Context, user int64, key string, now time.Time) error {
	if user <= 0 || s.DB == nil || s.Sealer == nil {
		return errors.New("mdblist: account storage unavailable")
	}
	if err := validateKey(key); err != nil {
		return err
	}
	sealed, err := s.seal(strings.TrimSpace(key), user)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockViewer(ctx, s.DB, tx, user); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO mdblist_accounts(user_id,api_key) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET api_key=excluded.api_key,snapshot='',backoff_until=0,last_warning='' WHERE mdblist_accounts.lease_until<?`, user, sealed, now.UnixMilli())
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrBusy
	}
	return tx.Commit()
}

// Load returns the viewer's key. A row that carries no longer opens (wrong
// key or moved to another viewer) is an error, never a silent empty key.
func (s TokenStore) Load(ctx context.Context, user int64) (Grant, error) {
	if user <= 0 || s.DB == nil || s.Sealer == nil {
		return Grant{}, errors.New("mdblist: account storage unavailable")
	}
	var stored string
	if err := s.DB.QueryRowContext(ctx, `SELECT api_key FROM mdblist_accounts WHERE user_id=?`, user).Scan(&stored); err != nil {
		return Grant{}, err
	}
	if !config.IsSealed(stored) {
		return Grant{}, errors.New("mdblist: unsealed API key")
	}
	key, _, err := s.Sealer.OpenField(stored, "mdblist_accounts", "api_key", config.FieldContext(user))
	if err != nil {
		return Grant{}, fmt.Errorf("mdblist: cannot open stored API key: %w", err)
	}
	return Grant{APIKey: key}, nil
}

// Delete removes a viewer's key and queue under the viewer lock. It is a
// no-op when nothing is stored, and refuses while a lease is held.
func (s TokenStore) Delete(ctx context.Context, user int64) error {
	if user <= 0 || s.DB == nil {
		return errors.New("mdblist: account storage unavailable")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockViewer(ctx, s.DB, tx, user); err != nil {
		return err
	}
	var sealed string
	err = tx.QueryRowContext(ctx, `SELECT api_key FROM mdblist_accounts WHERE user_id=?`, user).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM mdblist_accounts WHERE user_id=? AND api_key=? AND lease_until<?`, user, sealed, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrBusy
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM mdblist_outbox WHERE user_id=?`, user); err != nil {
		return err
	}
	return tx.Commit()
}

// Pop removes a viewer's key and queue, returning the key only when it
// actually removed the row. Used by merge/lifecycle bookkeeping.
func (s TokenStore) Pop(ctx context.Context, user int64) (Grant, error) {
	if user <= 0 || s.DB == nil || s.Sealer == nil {
		return Grant{}, errors.New("mdblist: account storage unavailable")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Grant{}, err
	}
	defer tx.Rollback()
	if err := lockViewer(ctx, s.DB, tx, user); err != nil {
		return Grant{}, err
	}
	var sealed string
	var leaseUntil int64
	err = tx.QueryRowContext(ctx, `SELECT api_key,lease_until FROM mdblist_accounts WHERE user_id=?`, user).Scan(&sealed, &leaseUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return Grant{}, tx.Commit()
	}
	if err != nil {
		return Grant{}, err
	}
	if leaseUntil >= time.Now().UnixMilli() {
		return Grant{}, ErrBusy
	}
	if !config.IsSealed(sealed) {
		return Grant{}, errors.New("mdblist: unsealed API key")
	}
	key, _, err := s.Sealer.OpenField(sealed, "mdblist_accounts", "api_key", config.FieldContext(user))
	if err != nil {
		return Grant{}, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM mdblist_accounts WHERE user_id=? AND api_key=? AND lease_until<?`, user, sealed, time.Now().UnixMilli())
	if err != nil {
		return Grant{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Grant{}, err
	}
	if n != 1 {
		return Grant{}, ErrBusy
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM mdblist_outbox WHERE user_id=?`, user); err != nil {
		return Grant{}, err
	}
	if err = tx.Commit(); err != nil {
		return Grant{}, err
	}
	return Grant{APIKey: key}, nil
}

func lockViewer(ctx context.Context, db *sql.DB, tx *sql.Tx, user int64) error {
	return store.LockKey(ctx, store.DialectOf(db), tx, fmt.Sprintf("mdblist-viewer:%d", user))
}
