package viewerpolicy

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/braedonsaunders/ether/internal/store"
)

// v1 gave PostgreSQL one statement trigger per policy table, and a
// statement trigger fires even when its statement touched no row: an empty
// DELETE, an INSERT ... ON CONFLICT DO NOTHING that inserted nothing, an
// UPDATE that matched nothing all advanced the epoch (an unchanged guide
// import still moved it). v7 splits each into per-event statement triggers
// with transition tables that advance only when rows changed. Tables a
// later version already gives row-level or conditional triggers are left
// alone. SQLite's row triggers already fire only per affected row.
var statementTables = []string{
	"auth_membership_epoch", "identity_links", "clusterconfig_shared",
	"personalize_home_layout", "personalize_overrides", "personalize_discovery_hidden",
	"favorites_items", "favorites_mirror", "notinterested_titles", "notinterested_revisions",
	"collections_specs", "collections_members", "collections_names", "collections_external", "collections_defaults",
	"state_playlists", "state_playlist_items", "state_playlist_markers",
	"livetv_policies", "livetv_channels", "livetv_sources", "livetv_programmes", "livetv_guide_channels",
	"safety_policies", "episodeorder_preferences", "episodeorder_groups", "auth_plex_defaults",
}

const pgRowsChanged = `
CREATE OR REPLACE FUNCTION viewer_policy_bump_inserted() RETURNS trigger LANGUAGE plpgsql AS $f$ BEGIN IF EXISTS (SELECT 1 FROM n) THEN PERFORM viewer_policy_advance(); END IF; RETURN NULL; END $f$;
CREATE OR REPLACE FUNCTION viewer_policy_bump_updated() RETURNS trigger LANGUAGE plpgsql AS $f$ BEGIN IF EXISTS (SELECT 1 FROM n) THEN PERFORM viewer_policy_advance(); END IF; RETURN NULL; END $f$;
`

func pgRowsChangedTriggers(table string) string {
	// auth_plex_defaults' v5 trigger has no event suffix either.
	update := fmt.Sprintf(`CREATE TRIGGER viewer_policy_%[1]s_update AFTER UPDATE ON %[1]s REFERENCING OLD TABLE AS o NEW TABLE AS n FOR EACH STATEMENT EXECUTE FUNCTION viewer_policy_bump_updated()`, table)
	return strings.Join([]string{
		fmt.Sprintf(`DROP TRIGGER IF EXISTS viewer_policy_%[1]s ON %[1]s`, table),
		fmt.Sprintf(`DROP TRIGGER IF EXISTS viewer_policy_%[1]s_insert ON %[1]s`, table),
		fmt.Sprintf(`DROP TRIGGER IF EXISTS viewer_policy_%[1]s_update ON %[1]s`, table),
		fmt.Sprintf(`DROP TRIGGER IF EXISTS viewer_policy_%[1]s_delete ON %[1]s`, table),
		fmt.Sprintf(`CREATE TRIGGER viewer_policy_%[1]s_insert AFTER INSERT ON %[1]s REFERENCING NEW TABLE AS n FOR EACH STATEMENT EXECUTE FUNCTION viewer_policy_bump_inserted()`, table),
		update,
		fmt.Sprintf(`CREATE TRIGGER viewer_policy_%[1]s_delete AFTER DELETE ON %[1]s REFERENCING OLD TABLE AS d FOR EACH STATEMENT EXECUTE FUNCTION viewer_policy_bump_deleted()`, table),
	}, "; ")
}

// auth_users keeps its column list on UPDATE (activity stamps are not
// policy), which transition tables do not allow; that event becomes a
// row trigger, still one advance per transaction.
func pgAuthUsersTriggers() string {
	var changed []string
	for _, c := range strings.Split(authPolicyColumns, ", ") {
		changed = append(changed, "OLD."+c+" IS DISTINCT FROM NEW."+c)
	}
	return strings.Join([]string{
		`DROP TRIGGER IF EXISTS viewer_policy_auth_users ON auth_users`,
		`DROP TRIGGER IF EXISTS viewer_policy_auth_users_insert ON auth_users`,
		`DROP TRIGGER IF EXISTS viewer_policy_auth_users_update ON auth_users`,
		`DROP TRIGGER IF EXISTS viewer_policy_auth_users_delete ON auth_users`,
		`CREATE TRIGGER viewer_policy_auth_users_insert AFTER INSERT ON auth_users REFERENCING NEW TABLE AS n FOR EACH STATEMENT EXECUTE FUNCTION viewer_policy_bump_inserted()`,
		`CREATE TRIGGER viewer_policy_auth_users_update AFTER UPDATE OF ` + authPolicyColumns + ` ON auth_users FOR EACH ROW WHEN (` + strings.Join(changed, " OR ") + `) EXECUTE FUNCTION viewer_policy_bump()`,
		`CREATE TRIGGER viewer_policy_auth_users_delete AFTER DELETE ON auth_users REFERENCING OLD TABLE AS d FOR EACH STATEMENT EXECUTE FUNCTION viewer_policy_bump_deleted()`,
	}, "; ")
}

func init() {
	store.Register("viewer_policy", 7, "SELECT 1;")
	store.RegisterSQLiteApply("viewer_policy", 7, func(context.Context, *sql.Tx) error { return nil })
	var pg strings.Builder
	pg.WriteString(pgRowsChanged)
	for _, t := range statementTables {
		pg.WriteString(pgGuard(t, pgRowsChangedTriggers(t)))
	}
	pg.WriteString(pgGuard("auth_users", pgAuthUsersTriggers()))
	store.RegisterDialect("viewer_policy", 7, store.Postgres, pg.String())
}
