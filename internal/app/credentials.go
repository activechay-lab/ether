package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// v0.3.4 startup conversion seals secrets from older installations before
// listeners open. Restarts resume interrupted conversions and handle
// restored backups. All new credentials are always written in v2.

func init() {
	// Heartbeated by every node (store.KnownMigrations): a release that
	// registers it reads sealed database columns. The table records when
	// the cluster's credentials were upgraded.
	store.Register("credentials", 1, `CREATE TABLE credentials_format (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  version INTEGER NOT NULL,
  upgraded_at INTEGER NOT NULL,
  key_fingerprint TEXT NOT NULL DEFAULT ''
);`)
}

// sealedTables are the database columns that hold sealed values.
var sealedTables = []struct{ table, column string }{
	{"simkl_devices", "device_code"},
	{"simkl_tokens", "access_token"},
	{"simkl_tokens", "refresh_token"},
	{"trakt_tokens", "access_token"},
	{"trakt_tokens", "refresh_token"},
	{"notify_channels", "config"},
	{"notify_push_keys", "private_key"},
	{"livetv_sources", "data"},
	{"cardigann_sessions", "cookies"},
	{"ratings_settings", "value"},
	{"watchlist_settings", "value"},
	{"clusterconfig_secrets", "value"},
	{"availability_releases", "payload"},
	{"availability_feed_items", "payload"},
}

// credentialKeyRejectedFile records, in the data directory, the
// fingerprint of a credential key that checkCredentialKey refused: Open
// had generated it while the database held values sealed with another.
// The key file it wrote is kept (nothing valid is deleted) and the marker
// keeps refusing it on later starts, until the original key is restored.
const credentialKeyRejectedFile = ".credentials-key-rejected"

var errCredentialKeyLost = errors.New("credentials: the database holds credentials sealed with a key this data directory does not have; restore the original .credentials-key (from the complete backup) before starting")

// checkCredentialKey refuses to start when this node's credential key is
// not the one the database's sealed values were written with:
//
//   - the database records the fingerprint of the key it was upgraded
//     with (credentials_format), and this key differs; or
//   - Open generated a new key (the key file was lost: a partial restore
//     or a copied database) while the database holds sealed values. The
//     refusal is recorded in credentialKeyRejectedFile, because the new
//     key file Open wrote would otherwise pass the next start.
//
// Starting would leave every stored credential unreadable while new ones
// are sealed with the wrong key.
func (a *App) checkCredentialKey(ctx context.Context) error {
	fp := a.cfg.KeyFingerprint()
	var recorded string
	err := a.db.QueryRowContext(ctx, `SELECT key_fingerprint FROM credentials_format WHERE id=1`).Scan(&recorded)
	switch {
	case err == nil && recorded != "" && recorded != fp:
		return errCredentialKeyLost
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		a.log.Warn("credentials: could not read the database's credential key fingerprint", "err", err)
	}
	marker := filepath.Join(filepath.Dir(a.cfg.Path()), credentialKeyRejectedFile)
	if b, err := os.ReadFile(marker); err == nil {
		if strings.TrimSpace(string(b)) == fp {
			return errCredentialKeyLost
		}
		// A different key now: the refused one was replaced. Check it
		// afresh below; the marker no longer applies.
		defer func() { _ = os.Remove(marker) }()
	}
	if !a.cfg.KeyCreated() {
		return nil
	}
	for _, t := range sealedTables {
		var n int
		err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+t.table+` WHERE `+t.column+` LIKE '%ether-sealed:%'`).Scan(&n)
		if err != nil {
			a.log.Warn("credentials: could not check for values sealed with another key", "table", t.table, "err", err)
			continue
		}
		if n > 0 {
			if err := os.WriteFile(marker, []byte(fp+"\n"), 0o600); err != nil {
				return fmt.Errorf("%w (and recording the refusal failed: %v)", errCredentialKeyLost, err)
			}
			return errCredentialKeyLost
		}
	}
	return nil
}

// migrateCredentials converts old stored secrets once before background work
// and listeners start. An interrupted pass resumes safely at the next startup.
func (a *App) migrateCredentials(ctx context.Context) error {
	store.WorkProgress(ctx, "settings", 0, 0)
	if err := a.recordCredentialFormat(ctx); err != nil {
		return err
	}
	if err := a.cfg.UpgradeCredentials(ctx); err != nil {
		return err
	}
	n, err := a.migrateStoredSecrets(ctx)
	if n > 0 {
		a.log.Info("credentials: sealed stored secrets", "rows", n)
	}
	return err
}

// recordCredentialFormat records the v2 upgrade and the fingerprint of the
// key the database is sealed with (the first node to upgrade sets it; all
// nodes share the key).
func (a *App) recordCredentialFormat(ctx context.Context) error {
	_, err := a.db.ExecContext(ctx, `INSERT INTO credentials_format(id, version, upgraded_at, key_fingerprint) VALUES(1, 2, ?, ?)
ON CONFLICT(id) DO UPDATE SET key_fingerprint=excluded.key_fingerprint WHERE credentials_format.key_fingerprint=''`,
		time.Now().Unix(), a.cfg.KeyFingerprint())
	return err
}

// migrateStoredSecrets runs every component's MigrateSecrets and purges
// search-cache rows that still hold indexer API keys. It returns the
// number of rows changed and the errors joined.
func (a *App) migrateStoredSecrets(ctx context.Context) (int, error) {
	total := 0
	var errs []error
	run := func(name string, fn func(context.Context) (int, error)) {
		store.WorkProgress(ctx, name, 0, 0)
		n, err := fn(ctx)
		total += n
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	if a.trakt != nil {
		run("trakt", a.trakt.MigrateSecrets)
	}
	if a.notifications != nil {
		run("notifications", a.notifications.MigrateSecrets)
	}
	if a.ratings != nil {
		run("ratings", a.ratings.MigrateSecrets)
	}
	if a.watchlist != nil {
		run("watchlist", a.watchlist.MigrateSecrets)
	}
	if a.livetv != nil {
		run("livetv", a.livetv.MigrateSecrets)
	}
	if a.sites != nil {
		indexers := a.cfg.Get().Indexers
		run("indexer sites", func(ctx context.Context) (int, error) { return a.sites.MigrateSecrets(ctx, indexers) })
	}
	if a.indexers != nil {
		run("search cache", func(ctx context.Context) (int, error) {
			n, err := a.indexers.PurgeLegacyCache(ctx)
			return int(n), err
		})
	}
	return total, errors.Join(errs...)
}

// migrateCredentialBlobs converts old identity blobs and cached releases in
// bounded transactions before consumers read them. Completed batches are skipped
// on restart. Updates use primary keys to avoid a table scan for every payload.
func (a *App) migrateCredentialBlobs(ctx context.Context) error {
	changed := 0
	for _, spec := range []struct{ table, column, name, keys string }{
		{"clusterconfig_secrets", "value", "", "name"},
		{"availability_releases", "payload", "availability-release", "id,rating_key,source_id"},
		{"availability_feed_items", "payload", "availability-release", "id,source_id"},
	} {
		store.WorkProgress(ctx, spec.table, 0, 0)
		keys := strings.Split(spec.keys, ",")
		rows, err := a.db.QueryContext(ctx, "SELECT "+spec.keys+","+spec.column+" FROM "+spec.table+" WHERE "+spec.column+" NOT LIKE 'ether-sealed:v2:%'")
		if err != nil {
			return err
		}
		type row struct {
			keys  []any
			value string
		}
		var all []row
		for rows.Next() {
			r := row{keys: make([]any, len(keys))}
			scan := make([]any, len(keys)+1)
			for i := range keys {
				scan[i] = &r.keys[i]
			}
			scan[len(keys)] = &r.value
			if err := rows.Scan(scan...); err != nil {
				rows.Close()
				return err
			}
			all = append(all, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		batch := store.NewUpgradeBatch(a.db)
		err = func() error {
			defer batch.Close()
			for i, r := range all {
				store.WorkProgress(ctx, spec.table, i, len(all))
				name := spec.name
				if name == "" {
					name = fmt.Sprint(r.keys[0])
				}
				sealed, err := a.cfg.ConvertLegacyBlob(name, r.value)
				if err != nil {
					return fmt.Errorf("convert %s: %w", spec.table, err)
				}
				query := "UPDATE " + spec.table + " SET " + spec.column + "=? WHERE "
				args := []any{sealed}
				for j, key := range keys {
					if j > 0 {
						query += " AND "
					}
					query += key + "=?"
					args = append(args, r.keys[j])
				}
				query += " AND " + spec.column + "=?"
				args = append(args, r.value)
				res, err := batch.ExecContext(ctx, query, args...)
				if err != nil {
					return err
				}
				n, _ := res.RowsAffected()
				changed += int(n)
			}
			if err := batch.Flush(); err != nil {
				return err
			}
			store.WorkProgress(ctx, spec.table, len(all), len(all))
			return nil
		}()
		if err != nil {
			return err
		}
	}
	if changed > 0 {
		a.log.Info("credentials: converted stored blobs to v2", "rows", changed)
	}
	return nil
}
