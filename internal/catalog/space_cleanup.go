package catalog

import (
	"context"
	"database/sql"
	"strings"

	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.RegisterAfter("catalog_space", "catalog", "catalog_tier_migrate")
	store.Register("catalog_space", 1, "SELECT 1;")
	store.RegisterDialect("catalog_space", 1, store.Postgres, "SELECT 1;")
	store.RegisterSQLiteApply("catalog_space", 1, applyCatalogSpaceSQLite)
	store.RegisterPostgresApply("catalog_space", 1, applyCatalogSpacePostgres)
}

func applyCatalogSpaceSQLite(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`DROP TRIGGER IF EXISTS catalog_person_names_credit_delete`,
		`CREATE TRIGGER catalog_person_names_credit_delete AFTER DELETE ON catalog_credits WHEN old.person_tmdb>0 BEGIN
 UPDATE catalog_person_names SET credits=credits-1 WHERE person_tmdb=old.person_tmdb;
 DELETE FROM catalog_person_names WHERE person_tmdb=old.person_tmdb AND credits<=0;
END`,
		`DROP TRIGGER IF EXISTS catalog_studio_names_delete`,
		`CREATE TRIGGER catalog_studio_names_delete AFTER DELETE ON catalog_studios BEGIN
 UPDATE catalog_studio_names SET titles=titles-1 WHERE studio=OLD.studio;
 DELETE FROM catalog_studio_names WHERE studio=OLD.studio AND titles<=0;
END`,
		`DROP TRIGGER IF EXISTS catalog_studio_names_update`,
		`CREATE TRIGGER catalog_studio_names_update AFTER UPDATE OF studio ON catalog_studios WHEN OLD.studio IS NOT NEW.studio BEGIN
 UPDATE catalog_studio_names SET titles=titles-1 WHERE studio=OLD.studio;
 DELETE FROM catalog_studio_names WHERE studio=OLD.studio AND titles<=0;
 INSERT INTO catalog_studio_names(studio,titles) VALUES (NEW.studio,1) ON CONFLICT(studio) DO UPDATE SET titles=catalog_studio_names.titles+1;
END`,
	}
	for _, q := range stmts {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	live := "a"
	var v string
	if err := tx.QueryRowContext(ctx, `SELECT v FROM catalog_meta WHERE k='rank_side'`).Scan(&v); err == nil && v == "b" {
		live = "b"
	}
	idle := map[string]string{"a": "b", "b": "a"}[live]
	idleTable := "catalog_rank_" + idle
	for _, q := range []string{`DROP TABLE IF EXISTS ` + idleTable, strings.TrimSuffix(rankTableSQL(idle, "INTEGER"), ";")} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return sweepZeroCountVocabTx(ctx, tx)
}

func applyCatalogSpacePostgres(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`CREATE OR REPLACE FUNCTION catalog_person_names_credit_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP = 'INSERT' THEN
  IF NEW.person_tmdb > 0 THEN
   INSERT INTO catalog_person_names(person_tmdb, name, credits) VALUES (NEW.person_tmdb, NEW.name, 1)
   ON CONFLICT(person_tmdb) DO UPDATE SET credits=catalog_person_names.credits+1,
    name=CASE WHEN EXCLUDED.name<>'' THEN EXCLUDED.name ELSE catalog_person_names.name END;
  END IF;
 ELSIF TG_OP = 'DELETE' THEN
  IF OLD.person_tmdb > 0 THEN
   UPDATE catalog_person_names SET credits=credits-1 WHERE person_tmdb=OLD.person_tmdb;
   DELETE FROM catalog_person_names WHERE person_tmdb=OLD.person_tmdb AND credits<=0;
  END IF;
 ELSIF NEW.person_tmdb > 0 AND NEW.name<>'' AND OLD.name IS DISTINCT FROM NEW.name THEN
  UPDATE catalog_person_names SET name=NEW.name WHERE person_tmdb=NEW.person_tmdb;
 END IF;
 RETURN NULL;
END $$`,
		`CREATE OR REPLACE FUNCTION catalog_studio_names() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP<>'INSERT' THEN
  UPDATE catalog_studio_names SET titles=titles-1 WHERE studio=OLD.studio;
  DELETE FROM catalog_studio_names WHERE studio=OLD.studio AND titles<=0;
 END IF;
 IF TG_OP<>'DELETE' THEN
  INSERT INTO catalog_studio_names(studio,titles) VALUES (NEW.studio,1) ON CONFLICT(studio) DO UPDATE SET titles=catalog_studio_names.titles+1;
 END IF;
 RETURN NULL;
END $$`,
	}
	for _, q := range stmts {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	live := "a"
	var v string
	if err := tx.QueryRowContext(ctx, `SELECT v FROM catalog_meta WHERE k='rank_side'`).Scan(&v); err == nil && v == "b" {
		live = "b"
	}
	idle := map[string]string{"a": "b", "b": "a"}[live]
	for _, q := range append(dropRankIndexesSQL(idle), `TRUNCATE catalog_rank_`+idle) {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return sweepZeroCountVocabTx(ctx, tx)
}

func sweepZeroCountVocabTx(ctx context.Context, tx *sql.Tx) error {
	var removed int64
	for _, q := range []string{
		`DELETE FROM catalog_person_names WHERE credits<=0`,
		`DELETE FROM catalog_studio_names WHERE titles<=0`,
	} {
		res, err := tx.ExecContext(ctx, q)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		removed += n
	}
	for _, q := range []string{
		`DELETE FROM catalog_facet_presence WHERE ranks=0 AND n<=0`,
		`DELETE FROM catalog_facet_tally WHERE n=0`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	if removed > 0 {
		return rebuildFacetIDs(ctx, tx)
	}
	return nil
}

func (c *Catalog) cleanupTierDerived(ctx context.Context) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := rebuildFacetTally(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_person_names WHERE credits<=0`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_studio_names WHERE titles<=0`); err != nil {
		return err
	}
	if err := rebuildFacetIDs(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}
