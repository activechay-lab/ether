package catalog

import "github.com/braedonsaunders/ether/internal/store"

// catalog 52: the most-credited order of catalog_person_names, so a people
// list without a name (Jellyfin and Emby /Persons) reads its first rows
// instead of sorting every credited person. Versions 47-51 belong to
// sibling migrations.
func init() {
	store.Register("catalog", 52, `CREATE INDEX IF NOT EXISTS catalog_person_names_credits ON catalog_person_names(credits DESC, person_tmdb)`)
}
