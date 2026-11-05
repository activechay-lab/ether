package newznab

import (
	"crypto/sha256"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/lru"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

const (
	maxTorrentDescriptors = 2048
	torrentDescriptorTTL  = time.Hour
)

type torrentDescriptor struct {
	hash, magnet string
	expires      time.Time
}

type torrentDescriptorCache struct {
	mu      sync.Mutex
	entries *lru.Cache[[32]byte, torrentDescriptor]
}

// Include the configuration and download identity, so replacing an indexer or
// its credentials cannot reuse an old descriptor. The cache stores no release
// targets, ranking, viewer information or negative availability decisions.
func torrentDescriptorKey(cfg config.Indexer, r core.Release) [32]byte {
	b, _ := json.Marshal(struct {
		Config        config.Indexer
		ID, GUID, URL string
	}{cfg, r.ID, r.GUID, r.NZBURL})
	return sha256.Sum256(b)
}

func (c *torrentDescriptorCache) get(key [32]byte, now time.Time) (string, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		return "", "", false
	}
	e, ok := c.entries.Get(key)
	if ok && !now.Before(e.expires) {
		c.entries.Remove(key)
		ok = false
	}
	return e.hash, e.magnet, ok
}

func (c *torrentDescriptorCache) put(key [32]byte, hash, magnet string, now time.Time) {
	// Trackers and display names come from external torrent documents. Do
	// not let one oversized magnet defeat the cache's memory bound.
	if len(magnet) > 16<<10 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = lru.New(lru.Options[[32]byte, torrentDescriptor]{MaxEntries: maxTorrentDescriptors})
	}
	c.entries.Add(key, torrentDescriptor{hash: hash, magnet: magnet, expires: now.Add(torrentDescriptorTTL)})
}
