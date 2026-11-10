package libraries

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// configNamesMigratedKey marks the one-time move of the old config
// library names into libraries 1/2 as done, so a later owner rename
// is never reverted by a stale config file.
const configNamesMigratedKey = "config_names_migrated"

// LegacyConfigNames extracts the pre-v0.3.8 library names from a raw
// config document (config.json bytes, or a cluster's shared server
// document): server.moviesLibraryName / server.tvShowsLibraryName.
// Missing keys come back empty; the config fields themselves are
// deleted, so this raw read is the only way the names survive (§1.3).
func LegacyConfigNames(configJSON []byte) (movies, shows string, err error) {
	var doc struct {
		Server struct {
			MoviesLibraryName  string `json:"moviesLibraryName"`
			TVShowsLibraryName string `json:"tvShowsLibraryName"`
		} `json:"server"`
	}
	if len(configJSON) == 0 {
		return "", "", nil
	}
	if err := json.Unmarshal(configJSON, &doc); err != nil {
		return "", "", err
	}
	return strings.TrimSpace(doc.Server.MoviesLibraryName), strings.TrimSpace(doc.Server.TVShowsLibraryName), nil
}

// MigrateConfigNames performs the one-time §1.3 migration: non-empty
// legacy names become the names of libraries 1 and 2. It runs at
// most once per database (libraries_state), so app wiring may call
// it on every open with whatever the config file still carries; once
// the config is rewritten without the old keys it becomes a no-op,
// and an owner rename in between is never overwritten.
func (s *Service) MigrateConfigNames(ctx context.Context, moviesName, showsName string) error {
	var done string
	err := s.db.QueryRowContext(ctx, `SELECT v FROM libraries_state WHERE k = ?`, configNamesMigratedKey).Scan(&done)
	if err == nil && done == "1" {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	now := s.now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, rename := range []struct {
		id   core.LibraryID
		name string
	}{
		{core.LibraryMovies, strings.TrimSpace(moviesName)},
		{core.LibraryShows, strings.TrimSpace(showsName)},
	} {
		if rename.name == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE libraries SET name = ?, updated_at = ? WHERE id = ? AND source = 'builtin'`,
			rename.name, now, int64(rename.id)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO libraries_state(k, v) VALUES(?, '1') ON CONFLICT(k) DO UPDATE SET v = '1'`,
		configNamesMigratedKey); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if strings.TrimSpace(moviesName) != "" || strings.TrimSpace(showsName) != "" {
		s.gen.Add(1)
		s.log.Info("libraries: migrated config library names", "movies", strings.TrimSpace(moviesName), "shows", strings.TrimSpace(showsName))
	}
	return nil
}
