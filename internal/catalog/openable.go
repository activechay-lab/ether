package catalog

import "github.com/braedonsaunders/ether/internal/core"

// TitleOpenable is the shared, pure admission predicate over loaded facts.
// No fetching, mirroring or database access belongs in this function.
func (c *Catalog) TitleOpenable(t *core.Title) bool {
	if t == nil {
		return false
	}
	// Virtual episode-order containers are owned by the episode-order adapter.
	if t.Type != core.TypeMovie && t.Type != core.TypeShow {
		return !t.Adult && !t.IsFutureRelease(c.now())
	}
	return c.visibleTitle(t) && !t.IsFutureRelease(c.now())
}
