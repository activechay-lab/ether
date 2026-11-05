package onair

import (
	"github.com/braedonsaunders/ether/internal/onair/scene"
	"github.com/braedonsaunders/ether/internal/store"
	"image"
	"strings"
	"sync"
	"time"
)

func init() {
	store.Register("onair", 2, `CREATE TABLE onair_feed_values(plugin TEXT NOT NULL,feed TEXT NOT NULL,data TEXT NOT NULL,fetched_at INTEGER NOT NULL,PRIMARY KEY(plugin,feed));`)
}

// PluginSegment is an immutable background-loaded snapshot. Lookup must do no I/O.
// Expiry gates playback and rendering, never the declared schedule duration.
type PluginSegment struct {
	Document scene.Document
	Slots    map[string]string
	Images   map[string]image.Image
	Version  string
	Expires  time.Time
	Rating   string
	Name     string
}

// PluginContentCache connects the host's background publisher to render readers.
// Values are immutable after publication; the cache owns the map itself.
type PluginContentCache struct {
	mu       sync.RWMutex
	values   map[string]PluginSegment
	disabled map[string]bool
}

func NewPluginContentCache() *PluginContentCache {
	return &PluginContentCache{values: map[string]PluginSegment{}, disabled: map[string]bool{}}
}
func (c *PluginContentCache) Lookup(id string) (PluginSegment, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.values[id]
	return v, ok
}
func (c *PluginContentCache) Publish(values map[string]PluginSegment) {
	c.mu.Lock()
	for id := range values {
		plugin, _, _ := strings.Cut(strings.TrimPrefix(id, "plugin:"), "/")
		if c.disabled[plugin] {
			delete(values, id)
		}
	}
	c.values = values
	c.mu.Unlock()
}

func (c *PluginContentCache) RemovePlugin(plugin string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disabled[plugin] = true
	for id := range c.values {
		if strings.HasPrefix(id, "plugin:"+plugin+"/") {
			delete(c.values, id)
		}
	}
}

func (c *PluginContentCache) EnablePlugin(plugin string) {
	c.mu.Lock()
	delete(c.disabled, plugin)
	c.mu.Unlock()
}
