// Package compactids allocates durable compact protocol identifiers.
package compactids

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("frontend_compact_ids", 1, `CREATE TABLE frontend_compact_id_counters (surface TEXT NOT NULL, kind TEXT NOT NULL, last_id INTEGER NOT NULL, PRIMARY KEY(surface,kind)); CREATE TABLE frontend_compact_ids (surface TEXT NOT NULL, kind TEXT NOT NULL, ref TEXT NOT NULL, id INTEGER NOT NULL, PRIMARY KEY(surface,kind,ref), UNIQUE(surface,kind,id));`)
}

type Allocator struct{ db *sql.DB }

func New(db *sql.DB) (*Allocator, error) {
	if db == nil {
		return nil, errors.New("compact ids: database required")
	}
	return &Allocator{db: db}, nil
}
func (a *Allocator) Compact(ctx context.Context, surface, kind, ref string) (int32, error) {
	if surface == "" || kind == "" || ref == "" {
		return 0, errors.New("compact ids: surface, kind and reference required")
	}
	var existing int64
	err := a.db.QueryRowContext(ctx, `SELECT id FROM frontend_compact_ids WHERE surface=? AND kind=? AND ref=?`, surface, kind, ref).Scan(&existing)
	if err == nil {
		return int32(existing), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO frontend_compact_id_counters(surface,kind,last_id) VALUES(?,?,1) ON CONFLICT(surface,kind) DO UPDATE SET last_id=frontend_compact_id_counters.last_id+1 WHERE frontend_compact_id_counters.last_id<2147483647 RETURNING last_id`, surface, kind).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		// Another allocator may have inserted this ref while we waited for the
		// counter lock, including the last available number.
		e := tx.QueryRowContext(ctx, `SELECT id FROM frontend_compact_ids WHERE surface=? AND kind=? AND ref=?`, surface, kind, ref).Scan(&id)
		if e == nil {
			if e = tx.Commit(); e != nil {
				return 0, e
			}
			return int32(id), nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return 0, e
		}
		return 0, errors.New("compact ids: namespace exhausted")
	}
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO frontend_compact_ids(surface,kind,ref,id) VALUES(?,?,?,?) ON CONFLICT(surface,kind,ref) DO NOTHING`, surface, kind, ref, id)
	if err != nil {
		return 0, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT id FROM frontend_compact_ids WHERE surface=? AND kind=? AND ref=?`, surface, kind, ref).Scan(&id); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return int32(id), nil
}
func (a *Allocator) Resolve(ctx context.Context, surface, kind string, id int32) (string, error) {
	if surface == "" || kind == "" || id <= 0 {
		return "", errors.New("compact ids: invalid identifier")
	}
	var ref string
	err := a.db.QueryRowContext(ctx, `SELECT ref FROM frontend_compact_ids WHERE surface=? AND kind=? AND id=?`, surface, kind, id).Scan(&ref)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("compact ids: not found")
	}
	return ref, err
}

// CompactBatch allocates a page in one transaction, with a single counter
// update. Existing identifiers are stable across concurrent export requests.
func (a *Allocator) CompactBatch(ctx context.Context, surface, kind string, refs []string) (map[string]int32, error) {
	if surface == "" || kind == "" || len(refs) > 250 {
		return nil, errors.New("compact ids: invalid batch")
	}
	result := map[string]int32{}
	if len(refs) == 0 {
		return result, nil
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// The namespace counter serializes allocators on SQLite and PostgreSQL.
	_, err = tx.ExecContext(ctx, `INSERT INTO frontend_compact_id_counters(surface,kind,last_id) VALUES(?,?,0) ON CONFLICT(surface,kind) DO UPDATE SET last_id=frontend_compact_id_counters.last_id`, surface, kind)
	if err != nil {
		return nil, err
	}
	args := []any{surface, kind}
	marks := make([]string, len(refs))
	for j, ref := range refs {
		if ref == "" {
			return nil, errors.New("compact ids: empty reference")
		}
		marks[j] = "?"
		args = append(args, ref)
	}
	rows, err := tx.QueryContext(ctx, `SELECT ref,id FROM frontend_compact_ids WHERE surface=? AND kind=? AND ref IN (`+strings.Join(marks, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ref string
		var id int32
		if err := rows.Scan(&ref, &id); err != nil {
			rows.Close()
			return nil, err
		}
		result[ref] = id
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	missing := []string{}
	seen := map[string]bool{}
	for _, ref := range refs {
		if result[ref] == 0 && !seen[ref] {
			missing = append(missing, ref)
			seen[ref] = true
		}
	}
	if len(missing) > 0 {
		var last int64
		err = tx.QueryRowContext(ctx, `UPDATE frontend_compact_id_counters SET last_id=last_id+? WHERE surface=? AND kind=? AND last_id<=2147483647-? RETURNING last_id`, len(missing), surface, kind, len(missing)).Scan(&last)
		if err != nil {
			return nil, err
		}
		vals := make([]string, len(missing))
		args = []any{}
		for j, ref := range missing {
			id := last - int64(len(missing)) + int64(j) + 1
			result[ref] = int32(id)
			vals[j] = "(?,?,?,?)"
			args = append(args, surface, kind, ref, id)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO frontend_compact_ids(surface,kind,ref,id) VALUES `+strings.Join(vals, ","), args...)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
