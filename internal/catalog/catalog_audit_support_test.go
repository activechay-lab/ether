package catalog

import (
	"time"
)

func WithStaleAfter(d time.Duration) Option {
	return func(c *Catalog) { c.staleAfter = d }
}
