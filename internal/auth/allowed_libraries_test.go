package auth

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// TestAllowedLibrariesMigration is the §12 slice-A acceptance for the
// auth side: a pre-v0.3.8 database (auth at version 8) keeps its
// section ids as library ids when auth/9 renames the column (§1.4).
func TestAllowedLibrariesMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ether.db")
	old, err := store.OpenPath(store.WithMigrationCeiling(ctx, map[string]int{"auth": 8}), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.ExecContext(ctx, `INSERT INTO auth_users(id, plex_id, username, allowed_sections, created_at, updated_at)
VALUES(5, 500, 'kid', '[2]', 100, 100)`); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := store.OpenPath(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var raw string
	if err := db.QueryRowContext(ctx, `SELECT allowed_libraries FROM auth_users WHERE id = 5`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "[2]" {
		t.Fatalf("allowed_libraries = %q, want the carried-over [2]", raw)
	}
	s, err := New(ctx, db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.ActiveUser(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.AllowedLibraries) != 1 || u.AllowedLibraries[0] != core.LibraryShows || u.AllowsLibrary(core.LibraryMovies) || !u.AllowsLibrary(core.LibraryShows) {
		t.Fatalf("migrated user = %+v", u)
	}
}
