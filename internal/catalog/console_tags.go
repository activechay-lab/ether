package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("catalog", 38, `CREATE TABLE catalog_owner_tags (key BIGINT NOT NULL, tag TEXT NOT NULL, PRIMARY KEY(key,tag)); CREATE INDEX catalog_owner_tags_value ON catalog_owner_tags(tag,key);`)
}

func (c *Catalog) TitleTags(ctx context.Context, key core.RatingKey) ([]string, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT tag FROM catalog_owner_tags WHERE key=? ORDER BY tag`, int64(key))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tags := []string{}
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

// SetTitleTags writes bounded owner annotations without changing provider facts.
func (c *Catalog) SetTitleTags(ctx context.Context, key core.RatingKey, tags []string) error {
	if len(tags) > 16 {
		return errors.New("use at most 16 tags")
	}
	clean := map[string]bool{}
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" || utf8.RuneCountInString(tag) > 64 {
			return errors.New("invalid tag")
		}
		clean[tag] = true
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var present int
	if err = tx.QueryRowContext(ctx, `SELECT 1 FROM catalog_titles WHERE key=?`, int64(key)).Scan(&present); errors.Is(err, sql.ErrNoRows) {
		return core.ErrNotFound
	} else if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_owner_tags WHERE key=?`, int64(key)); err != nil {
		return err
	}
	for tag := range clean {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_owner_tags(key,tag) VALUES(?,?)`, int64(key), tag); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	c.writeGen.Add(1)
	return nil
}
