package catalog

import (
	"context"
)

// refreshOneMovieYear fetches the next queued movie. Tests call it directly.
func (c *Catalog) refreshOneMovieYear(ctx context.Context) (bool, error) {
	key, ok, err := c.nextMovieYearKey(ctx)
	if !ok || err != nil {
		return false, err
	}
	return c.fetchMovieYear(ctx, key)
}
