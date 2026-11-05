package config

import (
	"strings"

	"github.com/braedonsaunders/ether/internal/dburl"
)

// DatabaseURLEnv overrides Database for this process: a postgres:// (or
// postgresql://) URL selects PostgreSQL with that URL. It lets container and
// service deployments keep credentials out of config.json.
const DatabaseURLEnv = "ETHER_DATABASE_URL"

const (
	DatabaseSQLite   = "sqlite"
	DatabasePostgres = "postgres"
)

// Database is this node's storage backend. It is node-local: a standalone
// server keeps SQLite in its data directory; a cluster node points at the
// owner-supplied PostgreSQL database. The URL carries credentials, so it is
// sealed in config.json like other secrets and must only ever be logged
// through Redacted.
type Database struct {
	// Driver is "sqlite" (default) or "postgres".
	Driver string `json:"driver,omitempty"`
	// URL is the PostgreSQL connection URL, e.g.
	// postgres://ether:secret@db.lan:5432/ether?sslmode=verify-full
	URL string `json:"url,omitempty"`
	// DeferMigrations makes this node wait, at startup, instead of applying
	// new schema migrations to the shared database while another live node
	// runs a version of Ether that does not know them. Off by default:
	// migrations are additive, so older nodes keep working on a newer
	// schema and a rolling update never stalls on it.
	DeferMigrations bool `json:"deferMigrations,omitempty"`
}

// Effective applies the ETHER_DATABASE_URL override (read through getenv)
// and normalises the driver name.
func (d Database) Effective(getenv func(string) string) Database {
	if getenv != nil {
		if v := strings.TrimSpace(getenv(DatabaseURLEnv)); v != "" && dburl.IsPostgres(v) {
			d.Driver, d.URL = DatabasePostgres, v
		}
	}
	switch strings.ToLower(strings.TrimSpace(d.Driver)) {
	case "postgres", "postgresql", "pg", "pgx":
		d.Driver = DatabasePostgres
	case "":
		if dburl.IsPostgres(d.URL) {
			d.Driver = DatabasePostgres
		} else {
			d.Driver = DatabaseSQLite
		}
	default:
		d.Driver = DatabaseSQLite
	}
	return d
}

// IsPostgres reports whether these (effective) settings select PostgreSQL.
func (d Database) IsPostgres() bool { return d.Effective(nil).Driver == DatabasePostgres }

// Redacted returns a copy safe to log or display: the URL's password is
// masked.
func (d Database) Redacted() Database {
	d.URL = dburl.Redact(d.URL)
	return d
}
