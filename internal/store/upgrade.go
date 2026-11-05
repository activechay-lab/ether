package store

import (
	"context"
	"database/sql"
)

type workKey struct{}

// WithWorkProgress attaches an upgrade observer without global state. Names
// are fixed operation IDs, never row identities or secret values.
func WithWorkProgress(ctx context.Context, fn func(string, int, int)) context.Context {
	return context.WithValue(ctx, workKey{}, fn)
}

func WorkProgress(ctx context.Context, name string, done, total int) {
	if fn, ok := ctx.Value(workKey{}).(func(string, int, int)); ok {
		fn(name, done, total)
	}
}

// UpgradeBatch bounds the number of writes per transaction. Committed batches
// survive interruption; callers retain their compare-and-swap predicates and
// skip already converted values on restart. Close rolls back only the tail.
type UpgradeBatch struct {
	db     *sql.DB
	tx     *sql.Tx
	writes int
}

func NewUpgradeBatch(db *sql.DB) *UpgradeBatch { return &UpgradeBatch{db: db} }
func (b *UpgradeBatch) Close() {
	if b.tx != nil {
		_ = b.tx.Rollback()
	}
}
func (b *UpgradeBatch) Flush() error {
	if b.tx == nil {
		return nil
	}
	err := b.tx.Commit()
	b.tx = nil
	b.writes = 0
	return err
}
func (b *UpgradeBatch) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if b.tx == nil {
		var err error
		b.tx, err = b.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
	}
	result, err := b.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	b.writes++
	if b.writes >= 500 {
		if err := b.Flush(); err != nil {
			return nil, err
		}
	}
	return result, nil
}
