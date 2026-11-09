package collections

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("collections", 5, `CREATE TABLE collections_defaults (name TEXT PRIMARY KEY, spec_id INTEGER NOT NULL);`)
}

// Ether no longer creates genre and network collections on install; they are
// templates (templates_library.go). Collections earlier versions created stay
// exactly as the owner left them, and are re-pointed once at their template so
// applying a library bundle skips them instead of adding a duplicate.
func (s *Service) adoptLibraryDefaultTemplates(ctx context.Context) error {
	if s.meta(ctx, "library-defaults-templates-v1") != "" {
		return nil
	}
	byFacet := map[string]string{}
	for _, d := range libraryTemplateDefs {
		r := d.spec.Rules[0]
		var values []string
		if json.Unmarshal(r.Value, &values) != nil {
			var one string
			_ = json.Unmarshal(r.Value, &one)
			values = []string{one}
		}
		for _, v := range values {
			byFacet["ether-default:"+d.spec.MediaType+"."+r.Field+"."+strings.ToLower(v)] = d.id
		}
	}
	rows, err := s.loadRows(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = store.LockKey(ctx, store.DialectOf(s.db), tx, "collections:library-defaults-templates"); err != nil {
		return err
	}
	var done string
	err = tx.QueryRowContext(ctx, `SELECT v FROM collections_meta WHERE k='library-defaults-templates-v1'`).Scan(&done)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for _, r := range rows {
		id, ok := byFacet[r.spec.TemplateID]
		if r.builtin || !ok {
			continue
		}
		sp := r.spec
		sp.TemplateID = id
		raw, err := json.Marshal(sp)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE collections_specs SET spec_json=? WHERE id=?`, string(raw), r.id); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO collections_meta(k,v) VALUES('library-defaults-templates-v1','1')`); err != nil {
		return fmt.Errorf("collection upgrade: %w", err)
	}
	return tx.Commit()
}

// One-time upgrade of shipped feed collections. Custom collections keep every
// publication choice. Stable IDs remain valid for existing Home policies/links.
func (s *Service) separateDiscoveryCollections(ctx context.Context) error {
	if s.meta(ctx, "library-separation-v1") != "" {
		return nil
	}
	rows, err := s.loadRows(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = store.LockKey(ctx, store.DialectOf(s.db), tx, "collections:library-separation"); err != nil {
		return err
	}
	var done string
	err = tx.QueryRowContext(ctx, `SELECT v FROM collections_meta WHERE k='library-separation-v1'`).Scan(&done)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for _, r := range rows {
		if !r.builtin {
			continue
		}
		sp := r.spec
		sp.Targets.CollectionsTab = false
		raw, err := json.Marshal(sp)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE collections_specs SET spec_json=?,updated_at=? WHERE id=?`, string(raw), s.now().Unix(), r.id); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO collections_meta(k,v) VALUES('library-separation-v1','1')`); err != nil {
		return fmt.Errorf("collection upgrade: %w", err)
	}
	return tx.Commit()
}

// A local single-facet collection reads an inverted index instead of scanning
// the whole catalogue once for every network. Owners can edit it freely: more
// complex rules automatically use the ordinary collection evaluator.
func localFacetSpec(sp core.CollectionSpec) (string, bool) {
	if sp.ReleaseMode == "upcoming" || sp.ReleaseMode == "mixed" {
		return "", false
	}
	if !sp.LocalOnly || sp.Match != MatchAll || len(sp.Sources) != 1 || sp.Sources[0].Kind != "catalog" || len(sp.Rules) != 1 || len(sp.Exclude) != 0 || sp.Sort != "popularity" || !sp.SortDesc || sp.MediaType == MediaMixed {
		return "", false
	}
	r := sp.Rules[0]
	if (r.Field != "genre" && r.Field != "studio") || (r.Op != "is" && r.Op != "in") {
		return "", false
	}
	var value string
	if json.Unmarshal(r.Value, &value) != nil {
		return "", false
	}
	return sp.MediaType + "|" + r.Field + "|" + strings.ToLower(value), true
}
func (snap *snapshot) facetKeys(key string) []core.RatingKey {
	snap.facetsOnce.Do(func() {
		snap.facets = map[string][]core.RatingKey{}
		for _, e := range snap.order {
			for _, p := range []struct {
				name   string
				values []string
			}{{"genre", e.genresLC}, {"studio", e.studiosLC}} {
				for _, v := range p.values {
					k := string(e.typ) + "|" + p.name + "|" + v
					snap.facets[k] = append(snap.facets[k], e.key)
				}
			}
		}
	})
	return snap.facets[key]
}
