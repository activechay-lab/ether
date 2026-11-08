package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/braedonsaunders/ether/internal/backup"
	"github.com/braedonsaunders/ether/internal/store"
)

// tierSnapshotSlack is how long before the tier migration was armed the
// pre-migrate snapshot may have been written: the snapshot is taken before
// the upgrade's migrations run, and the arming migration runs after the
// catalogue's (index builds on a large library take minutes).
const tierSnapshotSlack = 6 * time.Hour

// wireTierMigration gives the catalogue's tier migration its backup gate.
func (a *App) wireTierMigration() {
	if a.catalog == nil || a.backups == nil {
		return
	}
	dbPath := ""
	if store.DialectOf(a.db) == store.SQLite {
		dbPath = filepath.Join(a.opts.DataDir, "ether.db")
	}
	backups := a.backups
	a.catalog.SetTierBackup(func(ctx context.Context, since time.Time) (string, error) {
		return tierBackup(ctx, backups, dbPath, since)
	})
}

type tierBackups interface {
	Snapshots(context.Context) ([]backup.Snapshot, error)
	Create(context.Context) (backup.Snapshot, error)
}

// tierBackup names a copy of the database from this upgrade: the SQLite
// pre-migrate snapshot the upgrade took, or a backup bundle created since
// the upgrade. With neither it takes a bundle.
func tierBackup(ctx context.Context, svc tierBackups, sqlitePath string, since time.Time) (string, error) {
	if sqlitePath != "" {
		snap := sqlitePath + store.PreMigrateSuffix
		if st, err := os.Stat(snap); err == nil && st.Size() > 0 && !st.ModTime().Before(since.Add(-tierSnapshotSlack)) {
			return filepath.Base(snap), nil
		}
	}
	if snaps, err := svc.Snapshots(ctx); err == nil {
		for _, s := range snaps {
			if !s.Created.Before(since) {
				return s.Name, nil
			}
		}
	}
	sn, err := svc.Create(ctx)
	if err != nil {
		return "", fmt.Errorf("take a backup: %w", err)
	}
	return sn.Name, nil
}
