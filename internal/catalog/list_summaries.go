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

func (c *Catalog) listSummary(key core.RatingKey) (core.Title, bool) {
	c.listSummaryMu.Lock()
	defer c.listSummaryMu.Unlock()
	entry, ok := c.listSummaries[key]
	if !ok || !entry.expires.After(c.now()) {
		delete(c.listSummaries, key)
		return core.Title{}, false
	}
	return cloneTitle(entry.title), true
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
