package debrid

import ()

// inUse reports open connections to addr (tests and diagnostics).
func (c *connCap) inUse(addr string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.open[addr]
}
