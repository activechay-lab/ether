package tmdb

import ()

func (c *respCache) len() int { return c.lru.Len() }
