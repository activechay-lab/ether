package catalog

import (
	"context"
	"database/sql"
	"maps"
	"math"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/store"
)

// Release matching needs exactly the same normalization as releaseparse; the
// browse sort key has different article, punctuation and original-title rules.
func init() {
	store.Register("catalog", 32, `SELECT 1;`)
	store.RegisterApply("catalog", 32, migrateReleaseNames)
}

func migrateReleaseNames(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `CREATE TABLE catalog_release_names (name TEXT NOT NULL, key BIGINT NOT NULL, PRIMARY KEY(name,key))`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX catalog_release_names_key ON catalog_release_names(key)`); err != nil {
		return err
	}
	var total int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_titles`).Scan(&total); err != nil {
		return err
	}
	done := 0
	store.WorkProgress(ctx, "release_names", done, total)
	last := int64(math.MinInt64)
	for {
		rows, err := tx.QueryContext(ctx, `SELECT key,type,title,original_title FROM catalog_titles WHERE key>? ORDER BY key LIMIT 500`, last)
		if err != nil {
			return err
		}
		type row struct {
			key                  int64
			typ, title, original string
		}
		var batch []row
		for rows.Next() {
			var r row
			if err = rows.Scan(&r.key, &r.typ, &r.title, &r.original); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			store.WorkProgress(ctx, "release_names", done, total)
			return nil
		}
		// One multi-row insert per batch: a large catalogue backfills in
		// thousands of statements, not one per name.
		var args []any
		var values strings.Builder
		// Keep the range query on the primary key. A type predicate can
		// select an unselective type index and re-sort the entire library
		// for every batch after older schema upgrades.
		for _, r := range batch {
			last = r.key
			if r.typ != "movie" && r.typ != "show" {
				continue
			}
			for _, name := range []string{r.title, r.original} {
				if name = releaseparse.NormalizeTitle(name); name == "" {
					continue
				}
				if len(args) > 0 {
					values.WriteByte(',')
				}
				values.WriteString("(?,?)")
				args = append(args, name, r.key)
			}
		}
		if len(args) > 0 {
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_release_names(name,key) VALUES `+values.String()+` ON CONFLICT(name,key) DO NOTHING`, args...); err != nil {
				return err
			}
		}
		done += len(batch)
		store.WorkProgress(ctx, "release_names", done, total)
	}
}

func insertReleaseNames(ctx context.Context, tx *sql.Tx, key int64, names ...string) error {
	for _, name := range names {
		name = releaseparse.NormalizeTitle(name)
		if name == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_release_names(name,key) VALUES(?,?) ON CONFLICT(name,key) DO NOTHING`, name, key); err != nil {
			return err
		}
	}
	return nil
}

// updateReleaseNames keeps a movie's or show's release names in step with its
// stored titles. Seasons and episodes carry no release names, so their writes
// (most of a catalogue sync) cost nothing here, and an unchanged title is not
// rewritten.
func updateReleaseNames(ctx context.Context, tx *sql.Tx, key int64, typ core.MediaType) error {
	if typ != core.TypeMovie && typ != core.TypeShow {
		return nil
	}
	var title, original string
	if err := tx.QueryRowContext(ctx, `SELECT title,original_title FROM catalog_titles WHERE key=?`, key).Scan(&title, &original); err != nil {
		return err
	}
	return replaceReleaseNames(ctx, tx, key, title, original)
}

// insertFreshReleaseNames indexes a row this transaction just inserted with
// title and original: it has no names yet, and those are its stored names.
func insertFreshReleaseNames(ctx context.Context, tx *sql.Tx, key int64, typ core.MediaType, title, original string) error {
	if typ != core.TypeMovie && typ != core.TypeShow {
		return nil
	}
	return insertReleaseNames(ctx, tx, key, title, original)
}

func replaceReleaseNames(ctx context.Context, tx *sql.Tx, key int64, title, original string) error {
	want := map[string]bool{}
	for _, name := range []string{title, original} {
		if name = releaseparse.NormalizeTitle(name); name != "" {
			want[name] = true
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT name FROM catalog_release_names WHERE key=?`, key)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		have[name] = true
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if maps.Equal(have, want) {
		return nil
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_release_names WHERE key=?`, key); err != nil {
		return err
	}
	return insertReleaseNames(ctx, tx, key, title, original)
}
