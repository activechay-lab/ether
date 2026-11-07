package catalog

import "github.com/braedonsaunders/ether/internal/store"

// keysetIndex orders one type's visible titles by key, so a key-sorted page
// after a cursor (the library export walk) seeks to the cursor and stops at
// the page limit. Without it every page sorted the type's whole browse set,
// making a full export quadratic in library size. release_known and
// released_ts are carried so rejected rows never touch the table.
const keysetIndex = "catalog_titles_type_key_page"

func init() {
	store.Register("catalog", 42, `CREATE INDEX `+keysetIndex+` ON catalog_titles(type,browsable,adult,key,release_known,released_ts);`)
}
