package viewerpolicy

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// The epoch moves only when a write can turn some viewer's allow into a
// deny: a fence that passes must prove that nothing it authorized is now
// forbidden. A write that can only add what a viewer may see (a new title,
// a new credit) leaves it alone; the request that follows reads it anyway.
//
// Catalogue facts fall into three groups.
//
//   - Always policy: rating, adult, type, browse membership, release, episode
//     placement and collection (catalogPolicyColumns), the Plex share label
//     (collection_name), title and credit removal, home-release outcomes.
//   - Policy only while a rule reads them: genres, keywords, studios,
//     countries, language, year, runtime, rating, votes and popularity. Safety
//     policies block genres and keywords; library rules (global and
//     per-viewer) read the rest. viewer_policy_inputs lists what the
//     current rules read, so a detail fill on a server without such rules
//     moves nothing.
//   - Never policy: cast and crew details beyond membership, alternative
//     titles, collection artwork, raw home-release dates (their outcome is
//     catalog_home_release), summaries, artwork, fetch stamps.
//
// A viewer's own catalogue rules carry their own revision (Home and row
// views read catalog_viewer_rules.revision), so changing them does not
// invalidate every other viewer; what they read is still recorded here.
//
// viewer_policy_inputs(field, value, source) holds the union of what any
// rule source reads. value is a folded value ("horror"), "*" for any change
// to the field, or "" for a title's set of that field becoming empty or
// non-empty (library rules skip unknown facts, and an episode inherits its
// show's keywords only while it has none). Sources only ever add rows while
// running; RebuildInputs replaces a source wholesale under the inputs lock
// from durable state, so a stale row can only cost an extra advance.

// Conditional facet tables: table, value column and input field.
var facetInputs = []struct{ table, column, field string }{
	{"catalog_genres", "genre", "genre"},
	{"catalog_keywords", "keyword", "keyword"},
	{"catalog_studios", "studio", "studio"},
	{"catalog_countries", "country", "country"},
}

// Conditional catalog_titles columns and the library-rule field each feeds.
var titleRuleInputs = []struct{ column, field string }{
	{"year", "year"}, {"runtime_sec", "runtime"}, {"language", "language"},
	{"rating", "rating"}, {"vote_count", "votes"}, {"popularity", "popularity"},
}

// alwaysTitleColumns are the catalog_titles columns every viewer's policy
// reads: catalogPolicyColumns less the facts only library rules read
// (titleRuleInputs), plus the Plex share label.
func alwaysTitleColumns() []string {
	rule := map[string]bool{}
	for _, r := range titleRuleInputs {
		rule[r.column] = true
	}
	var out []string
	for _, c := range catalogPolicyColumns {
		if !rule[c] {
			out = append(out, c)
		}
	}
	return append(out, "collection_name")
}

// Fields whose values are matched one by one. Every other field records "*".
var valueFields = map[string]bool{"genre": true, "keyword": true}

const inputsLock = "viewer_policy:inputs"

// Inputs is a set of (field, value) rows for one source.
type Inputs map[[2]string]struct{}

// Field records that the policy reads field. For genre and keyword each
// value is recorded with the set's empty/non-empty transition; values a
// trigger cannot fold the way Go does (anything outside printable ASCII)
// degrade to "*". Other fields record "*".
func (in Inputs) Field(field string, values ...string) {
	if !valueFields[field] {
		in[[2]string{field, "*"}] = struct{}{}
		return
	}
	in[[2]string{field, ""}] = struct{}{}
	for _, v := range values {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" {
			continue
		}
		if !printableASCII(v) {
			v = "*"
		}
		in[[2]string{field, v}] = struct{}{}
	}
}

func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < ' ' || s[i] > '~' {
			return false
		}
	}
	return true
}

func (in Inputs) rows() [][2]string {
	out := make([][2]string, 0, len(in))
	for r := range in {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

// AddInputs records in for source inside the caller's transaction, which
// must also be the one that makes the policy durable. It never removes rows.
func AddInputs(ctx context.Context, db *sql.DB, tx *sql.Tx, source string, in Inputs) error {
	if len(in) == 0 {
		return nil
	}
	if err := store.LockKey(ctx, store.DialectOf(db), tx, inputsLock); err != nil {
		return err
	}
	for _, r := range in.rows() {
		if _, err := tx.ExecContext(ctx, `INSERT INTO viewer_policy_inputs(field,value,source) VALUES(?,?,?) ON CONFLICT DO NOTHING`, r[0], r[1], source); err != nil {
			return err
		}
	}
	return nil
}

// PublishConfig records the inputs of a newly published configuration and
// advances the epoch in one transaction (the config store's policy bump).
func PublishConfig(ctx context.Context, db *sql.DB, in Inputs) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := AddInputs(ctx, db, tx, SourceConfig, in); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE viewer_policy_epoch SET generation=generation+1 WHERE id=1`); err != nil {
		return err
	}
	return tx.Commit()
}

// Input sources.
const (
	SourceConfig = "config"
	SourceSafety = "safety"
	SourceRules  = "rules"
	sourceSeed   = "seed"
)

// Rebuild is one source's complete inputs, read inside the rebuild
// transaction so no concurrent writer can be lost. A nil Read leaves that
// source's rows (a cluster node whose config is not the shared current one).
type Rebuild struct {
	Source string
	Read   func(context.Context, *sql.Tx) (Inputs, error)
}

// RebuildInputs replaces each given source from durable state. Once every
// source has been rebuilt, the conservative seed rows the migration wrote go.
func RebuildInputs(ctx context.Context, db *sql.DB, sources ...Rebuild) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := store.LockKey(ctx, store.DialectOf(db), tx, inputsLock); err != nil {
		return err
	}
	complete := map[string]bool{}
	for _, s := range sources {
		if s.Read == nil {
			continue
		}
		in, err := s.Read(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM viewer_policy_inputs WHERE source=?`, s.Source); err != nil {
			return err
		}
		for _, r := range in.rows() {
			if _, err := tx.ExecContext(ctx, `INSERT INTO viewer_policy_inputs(field,value,source) VALUES(?,?,?) ON CONFLICT DO NOTHING`, r[0], r[1], s.Source); err != nil {
				return err
			}
		}
		complete[s.Source] = true
	}
	if complete[SourceConfig] && complete[SourceSafety] && complete[SourceRules] {
		// The seed stood in for every source until all three were known.
		// Dropping it can only narrow future advances; the epoch moves so
		// no fence spans the switch.
		res, err := tx.ExecContext(ctx, `DELETE FROM viewer_policy_inputs WHERE source=?`, sourceSeed)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE viewer_policy_epoch SET generation=generation+1 WHERE id=1`); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

const inputsSchema = `CREATE TABLE IF NOT EXISTS viewer_policy_inputs(field TEXT NOT NULL, value TEXT NOT NULL, source TEXT NOT NULL, PRIMARY KEY(field, value, source));`

func seedInputs() string {
	var b strings.Builder
	fields := []string{}
	for _, f := range facetInputs {
		fields = append(fields, f.field)
	}
	for _, f := range titleRuleInputs {
		fields = append(fields, f.field)
	}
	for _, f := range fields {
		fmt.Fprintf(&b, `INSERT INTO viewer_policy_inputs(field,value,source) VALUES('%s','*','%s') ON CONFLICT DO NOTHING;`, f, sourceSeed)
	}
	return b.String()
}

const sqliteBump = `UPDATE viewer_policy_epoch SET generation=generation+1 WHERE id=1;`

// sqliteFacetTriggers keeps facet rows policy only while a rule reads them.
func sqliteFacetTriggers(table, column, field string) string {
	inputs := func(row string) string {
		return fmt.Sprintf(`EXISTS (SELECT 1 FROM viewer_policy_inputs WHERE field='%[1]s' AND (value IN ('*', lower(trim(%[2]s.%[3]s))) OR %[2]s.%[3]s GLOB '*[^ -~]*' OR (value='' AND %[4]s)))`,
			field, row, column, "%s")
	}
	insert := fmt.Sprintf(inputs("new"), fmt.Sprintf(`NOT EXISTS (SELECT 1 FROM %s o WHERE o.key=new.key AND o.%s<>new.%s)`, table, column, column))
	del := fmt.Sprintf(inputs("old"), fmt.Sprintf(`NOT EXISTS (SELECT 1 FROM %s o WHERE o.key=old.key)`, table))
	return fmt.Sprintf(`DROP TRIGGER IF EXISTS viewer_policy_%[1]s_insert; DROP TRIGGER IF EXISTS viewer_policy_%[1]s_update; DROP TRIGGER IF EXISTS viewer_policy_%[1]s_delete;
CREATE TRIGGER viewer_policy_%[1]s_insert AFTER INSERT ON %[1]s WHEN %[2]s BEGIN %[5]s END;
CREATE TRIGGER viewer_policy_%[1]s_delete AFTER DELETE ON %[1]s WHEN %[3]s BEGIN %[5]s END;
CREATE TRIGGER viewer_policy_%[1]s_update AFTER UPDATE ON %[1]s WHEN (old.key IS NOT new.key OR old.%[4]s IS NOT new.%[4]s) AND EXISTS (SELECT 1 FROM viewer_policy_inputs WHERE field='%[6]s') BEGIN %[5]s END;
`, table, insert, del, column, sqliteBump, field)
}

func sqliteTitleTrigger() string {
	var core, rule []string
	for _, c := range alwaysTitleColumns() {
		core = append(core, "old."+c+" IS NOT new."+c)
	}
	for _, r := range titleRuleInputs {
		rule = append(rule, fmt.Sprintf(`(old.%s IS NOT new.%s AND EXISTS (SELECT 1 FROM viewer_policy_inputs WHERE field='%s'))`, r.column, r.column, r.field))
	}
	return `DROP TRIGGER IF EXISTS viewer_policy_catalog_titles_insert; DROP TRIGGER IF EXISTS viewer_policy_catalog_titles_update;
CREATE TRIGGER viewer_policy_catalog_titles_update AFTER UPDATE ON catalog_titles WHEN ` + strings.Join(core, " OR ") + ` OR ` + strings.Join(rule, " OR ") + ` BEGIN ` + sqliteBump + ` END;
`
}

// sqliteMembership: a credit or a title only adds what a viewer may see
// when it arrives; its removal (or a change of the pair it links) can take
// a person image or a credited title away.
const sqliteMembership = `DROP TRIGGER IF EXISTS viewer_policy_catalog_credits_insert; DROP TRIGGER IF EXISTS viewer_policy_catalog_credits_update;
CREATE TRIGGER viewer_policy_catalog_credits_update AFTER UPDATE OF key, person_tmdb ON catalog_credits WHEN old.key IS NOT new.key OR old.person_tmdb IS NOT new.person_tmdb BEGIN ` + sqliteBump + ` END;
`

func sqliteDropAll(table string) string {
	return fmt.Sprintf(`DROP TRIGGER IF EXISTS viewer_policy_%[1]s_insert; DROP TRIGGER IF EXISTS viewer_policy_%[1]s_update; DROP TRIGGER IF EXISTS viewer_policy_%[1]s_delete;`, table)
}

// PostgreSQL: one advance per transaction. A transaction-local setting
// remembers the advance (a savepoint rollback undoes both together), so a
// million-row fill updates the epoch row once instead of leaving a million
// dead versions of it.
const pgAdvance = `
CREATE OR REPLACE FUNCTION viewer_policy_advance() RETURNS void LANGUAGE plpgsql AS $$ BEGIN
 IF current_setting('ether.viewer_policy_advanced', true) IS DISTINCT FROM 'on' THEN
  UPDATE viewer_policy_epoch SET generation=generation+1 WHERE id=1;
  PERFORM set_config('ether.viewer_policy_advanced', 'on', true);
 END IF;
END $$;
CREATE OR REPLACE FUNCTION viewer_policy_bump() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM viewer_policy_advance(); RETURN NULL; END $$;
`

// pgFacetTriggers uses statement triggers with transition tables. An AFTER
// ROW trigger runs once the whole statement is done, so a multi-row INSERT
// could not tell that a title had no rows of the facet before it.
func pgFacetTriggers(table, column, field string) string {
	match := func(rows string) string {
		return fmt.Sprintf(`EXISTS (SELECT 1 FROM viewer_policy_inputs i WHERE i.field='%[1]s' AND (i.value='*' OR EXISTS (SELECT 1 FROM %[2]s r WHERE lower(btrim(r.%[3]s))=i.value OR r.%[3]s ~ '[^ -~]')`, field, rows, column)
	}
	insert := match("n") + fmt.Sprintf(` OR (i.value='' AND EXISTS (SELECT 1 FROM n WHERE NOT EXISTS (SELECT 1 FROM %[1]s o WHERE o.key=n.key AND NOT EXISTS (SELECT 1 FROM n n2 WHERE n2.key=o.key AND n2.%[2]s=o.%[2]s))))))`, table, column)
	del := match("d") + fmt.Sprintf(` OR (i.value='' AND EXISTS (SELECT 1 FROM d WHERE NOT EXISTS (SELECT 1 FROM %[1]s o WHERE o.key=d.key)))))`, table)
	update := fmt.Sprintf(`EXISTS (SELECT 1 FROM viewer_policy_inputs WHERE field='%s')`, field)
	fn := func(event, cond string) string {
		return fmt.Sprintf(`CREATE OR REPLACE FUNCTION viewer_policy_%[1]s_%[2]s() RETURNS trigger LANGUAGE plpgsql AS $f$ BEGIN IF %[3]s THEN PERFORM viewer_policy_advance(); END IF; RETURN NULL; END $f$;`, table, event, cond)
	}
	return fn("insert", insert) + fn("delete", del) + fn("update", update) + fmt.Sprintf(`
DROP TRIGGER IF EXISTS viewer_policy_%[1]s ON %[1]s;
DROP TRIGGER IF EXISTS viewer_policy_%[1]s_insert ON %[1]s; DROP TRIGGER IF EXISTS viewer_policy_%[1]s_delete ON %[1]s; DROP TRIGGER IF EXISTS viewer_policy_%[1]s_update ON %[1]s;
CREATE TRIGGER viewer_policy_%[1]s_insert AFTER INSERT ON %[1]s REFERENCING NEW TABLE AS n FOR EACH STATEMENT EXECUTE FUNCTION viewer_policy_%[1]s_insert();
CREATE TRIGGER viewer_policy_%[1]s_delete AFTER DELETE ON %[1]s REFERENCING OLD TABLE AS d FOR EACH STATEMENT EXECUTE FUNCTION viewer_policy_%[1]s_delete();
CREATE TRIGGER viewer_policy_%[1]s_update AFTER UPDATE ON %[1]s FOR EACH ROW WHEN (OLD.key IS DISTINCT FROM NEW.key OR OLD.%[2]s IS DISTINCT FROM NEW.%[2]s) EXECUTE FUNCTION viewer_policy_%[1]s_update();
`, table, column)
}

func pgTitleTriggers() string {
	var core, changed, rule []string
	for _, c := range alwaysTitleColumns() {
		core = append(core, "OLD."+c+" IS DISTINCT FROM NEW."+c)
	}
	for _, r := range titleRuleInputs {
		changed = append(changed, "OLD."+r.column+" IS DISTINCT FROM NEW."+r.column)
		rule = append(rule, fmt.Sprintf(`(OLD.%s IS DISTINCT FROM NEW.%s AND EXISTS (SELECT 1 FROM viewer_policy_inputs WHERE field='%s'))`, r.column, r.column, r.field))
	}
	return `CREATE OR REPLACE FUNCTION viewer_policy_catalog_titles_rules() RETURNS trigger LANGUAGE plpgsql AS $f$ BEGIN IF ` + strings.Join(rule, " OR ") + ` THEN PERFORM viewer_policy_advance(); END IF; RETURN NULL; END $f$;
DROP TRIGGER IF EXISTS viewer_policy_catalog_titles ON catalog_titles;
DROP TRIGGER IF EXISTS viewer_policy_catalog_titles_delete ON catalog_titles;
DROP TRIGGER IF EXISTS viewer_policy_catalog_titles_update ON catalog_titles;
DROP TRIGGER IF EXISTS viewer_policy_catalog_titles_rules ON catalog_titles;
CREATE TRIGGER viewer_policy_catalog_titles_delete AFTER DELETE ON catalog_titles REFERENCING OLD TABLE AS d FOR EACH STATEMENT EXECUTE FUNCTION viewer_policy_bump_deleted();
CREATE TRIGGER viewer_policy_catalog_titles_update AFTER UPDATE ON catalog_titles FOR EACH ROW WHEN (` + strings.Join(core, " OR ") + `) EXECUTE FUNCTION viewer_policy_bump();
CREATE TRIGGER viewer_policy_catalog_titles_rules AFTER UPDATE ON catalog_titles FOR EACH ROW WHEN (` + strings.Join(changed, " OR ") + `) EXECUTE FUNCTION viewer_policy_catalog_titles_rules();
`
}

// viewer_policy_bump_deleted advances only when the DELETE removed rows
// (statement triggers also fire for a DELETE that matched nothing).
const pgBumpDeleted = `CREATE OR REPLACE FUNCTION viewer_policy_bump_deleted() RETURNS trigger LANGUAGE plpgsql AS $f$ BEGIN IF EXISTS (SELECT 1 FROM d) THEN PERFORM viewer_policy_advance(); END IF; RETURN NULL; END $f$;
`

func pgMembership() string {
	return `DROP TRIGGER IF EXISTS viewer_policy_catalog_credits ON catalog_credits;
DROP TRIGGER IF EXISTS viewer_policy_catalog_credits_delete ON catalog_credits; DROP TRIGGER IF EXISTS viewer_policy_catalog_credits_update ON catalog_credits;
CREATE TRIGGER viewer_policy_catalog_credits_delete AFTER DELETE ON catalog_credits REFERENCING OLD TABLE AS d FOR EACH STATEMENT EXECUTE FUNCTION viewer_policy_bump_deleted();
CREATE TRIGGER viewer_policy_catalog_credits_update AFTER UPDATE OF key, person_tmdb ON catalog_credits FOR EACH ROW WHEN (OLD.key IS DISTINCT FROM NEW.key OR OLD.person_tmdb IS DISTINCT FROM NEW.person_tmdb) EXECUTE FUNCTION viewer_policy_bump();
`
}

// RulesInputs records what library rules (global or one viewer's) read.
// contentRating is always policy and needs no row.
func RulesInputs(in Inputs, specs []core.LibraryExclusion) {
	for _, spec := range specs {
		for _, r := range spec.Rules {
			field := r.Field
			if field == "network" {
				field = "studio" // networks are stored with studios
			}
			switch field {
			case "genre", "keyword":
				var values []string
				var one string
				if json.Unmarshal(r.Value, &one) == nil {
					values = []string{one}
				} else if json.Unmarshal(r.Value, &values) != nil {
					var objs []struct {
						Name string `json:"name"`
					}
					var obj struct {
						Name string `json:"name"`
					}
					if json.Unmarshal(r.Value, &objs) == nil {
						for _, o := range objs {
							values = append(values, o.Name)
						}
					} else if json.Unmarshal(r.Value, &obj) == nil {
						values = []string{obj.Name}
					} else if len(r.Value) > 0 && string(r.Value) != "null" {
						values = []string{"\x00"} // unreadable: any change
					}
				}
				in.Field(field, values...)
			case "contentRating":
			default:
				in.Field(field)
			}
		}
	}
}

func pgGuard(table, body string) string {
	return fmt.Sprintf(`DO $do$ BEGIN IF to_regclass('%s') IS NOT NULL THEN EXECUTE %s; END IF; END $do$;
`, table, pgQuote(body))
}

func pgQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func init() {
	store.Register("viewer_policy", 6, "SELECT 1;")
	store.RegisterSQLiteApply("viewer_policy", 6, func(ctx context.Context, tx *sql.Tx) error {
		exists := func(table string) (bool, error) {
			var n int
			err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n)
			return n > 0, err
		}
		var b strings.Builder
		b.WriteString(inputsSchema)
		b.WriteString(seedInputs())
		steps := []struct{ table, sql string }{
			{"catalog_titles", sqliteTitleTrigger()},
			{"catalog_credits", sqliteMembership},
			{"catalog_collections", sqliteDropAll("catalog_collections")},
			{"catalog_home_release_dates", sqliteDropAll("catalog_home_release_dates")},
		}
		for _, f := range facetInputs {
			steps = append(steps, struct{ table, sql string }{f.table, sqliteFacetTriggers(f.table, f.column, f.field)})
		}
		for _, s := range steps {
			ok, err := exists(s.table)
			if err != nil {
				return err
			}
			if ok {
				b.WriteString(s.sql)
			}
		}
		_, err := tx.ExecContext(ctx, b.String())
		return err
	})
	var pg strings.Builder
	pg.WriteString(inputsSchema)
	pg.WriteString(seedInputs())
	pg.WriteString(pgAdvance)
	pg.WriteString(pgBumpDeleted)
	pg.WriteString(pgGuard("catalog_titles", pgTitleTriggers()))
	pg.WriteString(pgGuard("catalog_credits", pgMembership()))
	for _, table := range []string{"catalog_collections", "catalog_home_release_dates"} {
		pg.WriteString(pgGuard(table, fmt.Sprintf(`DROP TRIGGER IF EXISTS viewer_policy_%[1]s ON %[1]s`, table)))
	}
	for _, f := range facetInputs {
		pg.WriteString(pgGuard(f.table, pgFacetTriggers(f.table, f.column, f.field)))
	}
	store.RegisterDialect("viewer_policy", 6, store.Postgres, pg.String())
}
