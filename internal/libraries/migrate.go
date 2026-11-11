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

// configRulesMigratedKey marks the one-time move of global
// catalog.exclusions + visibilityDefault into libraries 1/2 as done.
const configRulesMigratedKey = "config_rules_migrated"

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

// MigrateConfigRules performs the one-time migration of global
// catalog.exclusions and catalog.visibilityDefault into built-in libraries
// 1 (Movies) and 2 (TV Shows): rules scoped to one media type move only to
// that library, while unscoped ("mixed"/"") rules move to both. Runs at most
// once per database so later owner edits on either library are never reverted.
func (s *Service) MigrateConfigRules(ctx context.Context, rules []core.LibraryExclusion, visibilityDefault string) error {
	var done string
	err := s.db.QueryRowContext(ctx, `SELECT v FROM libraries_state WHERE k = ?`, configRulesMigratedKey).Scan(&done)
	if err == nil && done == "1" {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var movieRules, showRules []core.LibraryExclusion
	for _, r := range rules {
		switch r.MediaType {
		case string(core.TypeMovie):
			cp := cloneRule(r)
			cp.MediaType = string(core.TypeMovie)
			movieRules = append(movieRules, cp)
		case string(core.TypeShow):
			cp := cloneRule(r)
			cp.MediaType = string(core.TypeShow)
			showRules = append(showRules, cp)
		default:
			m := cloneRule(r)
			m.MediaType = string(core.TypeMovie)
			movieRules = append(movieRules, m)
			sh := cloneRule(r)
			sh.MediaType = string(core.TypeShow)
			showRules = append(showRules, sh)
		}
	}
	vis := ""
	if visibilityDefault == "hide" {
		vis = "hide"
	}
	now := s.now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	changed := false
	for _, target := range []struct {
		id    core.LibraryID
		rules []core.LibraryExclusion
	}{
		{core.LibraryMovies, movieRules},
		{core.LibraryShows, showRules},
	} {
		if len(target.rules) == 0 && vis == "" {
			continue
		}
		rulesJSON := "[]"
		if len(target.rules) > 0 {
			b, err := json.Marshal(target.rules)
			if err != nil {
				return err
			}
			rulesJSON = string(b)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE libraries SET rules = ?, visibility_default = ?, updated_at = ? WHERE id = ? AND source = 'builtin' AND rules = '[]' AND visibility_default = ''`,
			rulesJSON, vis, now, int64(target.id)); err != nil {
			return err
		}
		changed = true
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO libraries_state(k, v) VALUES(?, '1') ON CONFLICT(k) DO UPDATE SET v = '1'`,
		configRulesMigratedKey); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if changed {
		s.gen.Add(1)
		s.log.Info("libraries: migrated global catalog rules", "movies", len(movieRules), "shows", len(showRules), "default", vis)
	}
	return nil
}
