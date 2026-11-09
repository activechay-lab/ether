package catalog

import (
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// remoteSearchTTL bounds how long a finished TMDB search answers the same
// query without asking TMDB again.
const remoteSearchTTL = 10 * time.Minute

// remoteSearchLimit bounds the remembered queries.
const remoteSearchLimit = 512

// remoteSearchCache remembers finished TMDB searches by query key. Search
// hits are summaries kept in memory, never stored, so a search that ran
// detached (the mirror answered at once) is only useful to the next search
// through this cache.
type remoteSearchCache struct {
	mu      sync.Mutex
	entries map[string]remoteSearch
}

type remoteSearch struct {
	titles  []core.Title
	expires time.Time
}

func (r *remoteSearchCache) get(key string, now time.Time) ([]core.Title, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[key]
	if !ok || !e.expires.After(now) {
		return nil, false
	}
	out := make([]core.Title, len(e.titles))
	for i, t := range e.titles {
		out[i] = cloneTitle(t)
	}
	return out, true
}

func (r *remoteSearchCache) put(key string, titles []core.Title, now time.Time) {
	kept := make([]core.Title, len(titles))
	for i, t := range titles {
		kept[i] = cloneTitle(t)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = map[string]remoteSearch{}
	}
	if _, ok := r.entries[key]; !ok && len(r.entries) >= remoteSearchLimit {
		for k, e := range r.entries {
			if !e.expires.After(now) {
				delete(r.entries, k)
			}
		}
		if len(r.entries) >= remoteSearchLimit {
			clear(r.entries)
		}
	}
	r.entries[key] = remoteSearch{kept, now.Add(remoteSearchTTL)}
}

// reset forgets every search (the TMDB client changed).
func (r *remoteSearchCache) reset() {
	r.mu.Lock()
	clear(r.entries)
	r.mu.Unlock()
}
