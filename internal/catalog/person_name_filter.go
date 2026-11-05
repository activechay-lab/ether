package catalog

import (
	"strings"

	"github.com/braedonsaunders/ether/internal/store"
)

// SQLite's NOCASE contract folds ASCII only. PostgreSQL's locale-aware
// lower must not fold accented names that PMS treats as distinct.
func personNameExpression(d store.Dialect) string {
	if d == store.Postgres {
		return `translate(name, 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz') COLLATE "C"`
	}
	return "LOWER(name)"
}
func asciiPersonName(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}
func init() {
	store.Register("catalog", 33, `CREATE INDEX catalog_person_names_exact ON catalog_person_names(LOWER(name), person_tmdb);`)
	store.RegisterDialect("catalog", 33, store.Postgres, `CREATE INDEX catalog_person_names_exact ON catalog_person_names((translate(name, 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz') COLLATE "C"), person_tmdb);`)
}
