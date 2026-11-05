package catalog

import (
	"context"
	"github.com/braedonsaunders/ether/internal/store"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// listMirrorEvery is how often a related list's already stored summaries are
// saved again. The catalogue refresh keeps stored rows current; this only
// carries TMDB's list-time popularity and threshold changes into them.
const listMirrorEvery = 24 * time.Hour

// listMirrorMax bounds the remembered list stamps.
const listMirrorMax = 4096

// mirrorListSummaries saves the summaries of a related list (recommendations,
// similar titles, a franchise) that a read is about to show. Titles the
// mirror lacks are saved now in one transaction: the read needs their rows.
// Titles it already holds are served as stored and saved again behind the
// read at most once per listMirrorEvery, so a detail page or post-play
// request that shows a known list writes nothing. Saving every list on every
// view rewrote thirty to a hundred rows per request and moved the catalogue
// generation, which dropped every cached browse page and total.
//
// browsable is how the titles are saved: search results join the browsable
// set, so for them a stored row that is not browsable yet counts as missing.
func (c *Catalog) mirrorListSummaries(ctx context.Context, list string, titles []core.Title, browsable bool) error {
	if len(titles) == 0 {
		return nil
	}
	stored, err := c.storedKeys(ctx, titles, browsable)
	if err != nil {
		return err
	}
	var missing []core.Title
	for _, t := range titles {
		if !stored[t.Key] {
			missing = append(missing, t)
		}
	}
	save := func(ctx context.Context, titles []core.Title) error {
		if browsable {
			return c.upsertMany(ctx, titles, upsertOpts{browsable: true})
		}
		return c.upsertSummaries(ctx, titles, false)
	}
	if len(missing) > 0 {
		if err := save(ctx, missing); err != nil {
			return err
		}
	}
	if len(missing) == len(titles) {
		c.stampListMirrored(list)
		return nil
	}
	if !c.listMirrorDue(list) {
		return nil
	}
	all := append([]core.Title(nil), titles...)
	_ = c.hydrate(ctx, "list:"+list, 0, func(ctx context.Context) error {
		if err := save(ctx, all); err != nil {
			return err
		}
		c.stampListMirrored(list)
		return nil
	})
	return nil
}

// storedKeys reports which of titles have a catalogue row (a browsable one
// when browsable is set).
func (c *Catalog) storedKeys(ctx context.Context, titles []core.Title, browsable bool) (map[core.RatingKey]bool, error) {
	cond := ""
	if browsable {
		cond = " AND browsable=1"
	}
	out := make(map[core.RatingKey]bool, len(titles))
	const batch = 200
	for chunk := range store.Chunks(titles, batch) {
		part := chunk
		args := make([]any, len(part))
		for i := range part {
			args[i] = int64(part[i].Key)
		}
		rows, err := c.db.QueryContext(ctx, `SELECT key FROM catalog_titles WHERE key IN (`+placeholders(len(args))+`)`+cond, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key int64
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return nil, err
			}
			out[core.RatingKey(key)] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// listMirrorDue reports whether a list's stored summaries are due to be saved
// again. A list first seen by this process starts its period now: the stamps
// live in memory, and treating every list as due after a restart rewrote each
// detail page's lists on its first view.
func (c *Catalog) listMirrorDue(list string) bool {
	c.personMu.Lock()
	defer c.personMu.Unlock()
	at, ok := c.listMirrored[list]
	if !ok {
		if len(c.listMirrored) >= listMirrorMax {
			c.listMirrored = map[string]time.Time{}
		}
		c.listMirrored[list] = c.now()
		return false
	}
	return c.now().Sub(at) >= listMirrorEvery
}

func (c *Catalog) stampListMirrored(list string) {
	c.personMu.Lock()
	defer c.personMu.Unlock()
	if len(c.listMirrored) >= listMirrorMax {
		c.listMirrored = map[string]time.Time{}
	}
	c.listMirrored[list] = c.now()
}

// saveCollection stores a franchise's own fields when they are new or
// changed; a view of an unchanged franchise writes nothing.
func (c *Catalog) saveCollection(ctx context.Context, id int64, name, summary, poster, backdrop string) {
	var oldName, oldSummary, oldPoster, oldBackdrop string
	err := c.db.QueryRowContext(ctx, `SELECT name,summary,poster,backdrop FROM catalog_collections WHERE tmdb_id=?`, id).Scan(&oldName, &oldSummary, &oldPoster, &oldBackdrop)
	if err == nil && oldName == name && oldSummary == summary && oldPoster == poster && oldBackdrop == backdrop {
		return
	}
	_, _ = c.db.ExecContext(ctx, `INSERT INTO catalog_collections(tmdb_id,name,summary,poster,backdrop,fetched_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(tmdb_id) DO UPDATE SET name=excluded.name,summary=excluded.summary,poster=excluded.poster,backdrop=excluded.backdrop,fetched_at=excluded.fetched_at`,
		id, name, summary, poster, backdrop, c.now().Unix())
}
