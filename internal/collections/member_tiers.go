package collections

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

type titlePromoter interface {
	PromoteTitles(ctx context.Context, keys []core.RatingKey) error
}

// promoteMembers ensures released collection members in catalog_titles belong
// to the library tier (browsable=1), even when they originally entered the
// database as search-only / on-demand long-tail rows (browsable=0).
func (s *Service) promoteMembers(ctx context.Context, keys []core.RatingKey) {
	if len(keys) == 0 {
		return
	}
	var p titlePromoter
	if tp, ok := s.cat.(titlePromoter); ok {
		p = tp
	} else if tp, ok := s.upserter.(titlePromoter); ok {
		p = tp
	}
	if p != nil {
		if err := p.PromoteTitles(ctx, keys); err != nil {
			s.log.Debug("collections: promote members", "err", err)
			return
		}
	}
	if snap := s.idx.current(); snap != nil {
		var missing []core.RatingKey
		for _, k := range keys {
			if snap.entries[k] == nil {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			if ts, err := s.mirror.Titles(ctx, missing); err == nil && len(ts) > 0 {
				s.idx.add(ts)
			}
		}
	}
}

// repairMemberTiers heals existing collection members left in the search-only
// tier (browsable=0) or missing a catalog_titles row after a catalogue tier
// migration.
func (s *Service) repairMemberTiers(ctx context.Context) {
	if s.repairedTiers.Load() {
		return
	}
	// Collections reads only its own table: the catalogue decides which of
	// these rows it may promote, and the mirror says which have no row yet.
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT key FROM collections_members`)
	if err != nil {
		return
	}
	var members []core.RatingKey
	for rows.Next() {
		var k int64
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return
		}
		members = append(members, core.RatingKey(k))
	}
	rows.Close()
	if rows.Err() != nil {
		return
	}
	if len(members) == 0 {
		s.repairedTiers.Store(true)
		return
	}
	s.promoteMembers(ctx, members)
	var missing []core.RatingKey
	const lookupBatch = 500
	for start := 0; start < len(members); start += lookupBatch {
		if ctx.Err() != nil {
			return
		}
		chunk := members[start:min(start+lookupBatch, len(members))]
		stored, err := s.mirror.ValidKeys(ctx, chunk, true)
		if err != nil {
			return
		}
		have := make(map[core.RatingKey]bool, len(stored))
		for _, k := range stored {
			have[k] = true
		}
		for _, k := range chunk {
			if !have[k] {
				missing = append(missing, k)
			}
		}
	}
	complete := true
	if len(missing) > 0 && s.cat != nil {
		const maxFetch = 500
		if len(missing) > maxFetch {
			missing = missing[:maxFetch]
			complete = false
		}
		var fetched []core.Title
		var fetchedKeys []core.RatingKey
		for _, k := range missing {
			if ctx.Err() != nil {
				return
			}
			t, err := s.cat.Get(ctx, k)
			if err != nil || t == nil {
				continue
			}
			fetched = append(fetched, *t)
			fetchedKeys = append(fetchedKeys, t.Key)
		}
		if len(fetched) > 0 {
			if s.upserter != nil {
				if batch, ok := s.upserter.(interface {
					UpsertCollectionTitles(context.Context, []core.Title) error
				}); ok {
					_ = batch.UpsertCollectionTitles(ctx, fetched)
				} else {
					for _, t := range fetched {
						_ = s.upserter.UpsertTitle(ctx, t, t.IsReleased(s.now()))
					}
				}
			}
			s.promoteMembers(ctx, fetchedKeys)
			var released []core.Title
			now := s.now()
			for _, t := range fetched {
				if t.IsReleased(now) {
					released = append(released, t)
				}
			}
			if len(released) > 0 {
				s.idx.add(released)
			}
		}
	}
	if complete && ctx.Err() == nil {
		s.repairedTiers.Store(true)
	}
}

// snapshotForServe returns an index snapshot covering keys without blocking
// built-collection serving on a full-catalogue index rebuild. When the
// background index is still building (or lacks non-browsable/upcoming members),
// only the collection's own missing keys are hydrated by primary key.
func (s *Service) snapshotForServe(ctx context.Context, keys []core.RatingKey, includeFuture bool) (*snapshot, error) {
	var snap *snapshot
	if s.idx.checkTTL <= 0 && s.idx.current() != nil {
		var err error
		snap, err = s.idx.get(ctx)
		if err != nil {
			return nil, err
		}
	} else {
		snap = s.idx.ready()
	}
	if snap == nil {
		snap = &snapshot{entries: map[core.RatingKey]*entry{}}
	}
	if includeFuture {
		titles, err := s.mirror.TitlesIncludingFuture(ctx, keys)
		if err != nil {
			return nil, err
		}
		in := newInterner()
		entries := make([]*entry, 0, len(titles))
		for _, t := range titles {
			entries = append(entries, entryFromTitle(t, in))
		}
		return mergeEntries(snap, entries), nil
	}
	var missing []core.RatingKey
	for _, k := range keys {
		if snap.entries[k] == nil {
			missing = append(missing, k)
		}
	}
	if len(missing) == 0 {
		return snap, nil
	}
	titles, err := s.mirror.Titles(ctx, missing)
	if err != nil {
		return nil, err
	}
	if len(titles) == 0 {
		return snap, nil
	}
	in := newInterner()
	entries := make([]*entry, 0, len(titles))
	for _, t := range titles {
		entries = append(entries, entryFromTitle(t, in))
	}
	return mergeEntries(snap, entries), nil
}
