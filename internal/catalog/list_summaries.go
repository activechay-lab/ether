package catalog

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/core"
	"time"
)

type listSummary struct {
	title   core.Title
	expires time.Time
}

const listSummaryLimit = 4096

// After the in-memory list summaries it asks this node's long-tail index,
// so a title found by search on one node opens at once on any other: Get
// serves the summary and fetches the details behind it (the promotion). A
// tail entry whose date is not known yet is not served: Get waits for the
// details, which carry the date its visibility needs.
func (c *Catalog) listSummary(key core.RatingKey) (core.Title, bool) {
	c.listSummaryMu.Lock()
	entry, ok := c.listSummaries[key]
	if ok && entry.expires.After(c.now()) {
		t := cloneTitle(entry.title)
		c.listSummaryMu.Unlock()
		return t, true
	}
	if ok {
		delete(c.listSummaries, key)
	}
	c.listSummaryMu.Unlock()
	if k := key.Kind(); k != core.KindMovie && k != core.KindShow {
		return core.Title{}, false
	}
	if e, ok := c.tailEntry(key); ok && !e.DateUnknown && !e.Title.Released.IsZero() {
		return e.Title, true
	}
	return core.Title{}, false
}

// RememberListSummaries makes fresh source summaries readable immediately.
// The bounded existing hydration scheduler owns all mirroring behind the read;
// no caller waits for its writer or carries request QueryStats into that work.
func (c *Catalog) RememberListSummaries(ctx context.Context, titles []core.Title) {
	if len(titles) == 0 {
		return
	}
	accepted := c.rememberListSummaries(titles)
	if len(accepted) == 0 {
		return
	}
	key := fmt.Sprintf("list-summaries:%s:%d", accepted[0].Key, len(accepted))
	_ = c.hydrate(ctx, key, 0, func(bg context.Context) error {
		stored, err := c.storedKeys(bg, accepted, false)
		if err != nil {
			return err
		}
		missing := make([]core.Title, 0, len(accepted))
		for _, t := range accepted {
			if !stored[t.Key] {
				missing = append(missing, t)
			}
		}
		return c.upsertSummaryBatch(bg, missing, false, true)
	})
}

// rememberListSummaries registers source data without starting a second writer.
func (c *Catalog) rememberListSummaries(titles []core.Title) []core.Title {
	c.listSummaryMu.Lock()
	if c.listSummaries == nil {
		c.listSummaries = map[core.RatingKey]listSummary{}
	}
	now := c.now()
	if len(c.listSummaries)+len(titles) > listSummaryLimit {
		for key, entry := range c.listSummaries {
			if !entry.expires.After(now) {
				delete(c.listSummaries, key)
			}
		}
	}
	accepted := make([]core.Title, 0, len(titles))
	for _, t := range titles {
		if t.Type != core.TypeMovie && t.Type != core.TypeShow || t.Key == 0 {
			continue
		}
		if _, ok := c.listSummaries[t.Key]; !ok && len(c.listSummaries) >= listSummaryLimit {
			continue
		}
		t = cloneTitle(t)
		c.listSummaries[t.Key] = listSummary{t, now.Add(time.Hour)}
		accepted = append(accepted, t)
	}
	c.listSummaryMu.Unlock()
	return accepted
}
