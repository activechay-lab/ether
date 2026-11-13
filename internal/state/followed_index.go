package state

import "github.com/braedonsaunders/ether/internal/store"

func init() {
	store.RegisterAfter("state_followed", "state")
	store.Register("state_followed", 1, `CREATE INDEX state_watch_followed ON state_watch(last_viewed_at DESC,rating_key) WHERE last_viewed_at>0;`)
}
