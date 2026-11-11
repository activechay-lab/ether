package availability

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// RecordSearch retains descriptors from an actual requested search. It does not
// initiate searches, grab media or claim health. Post-date accepted matches
// open availability_gate immediately; pre-date matches queue verification.
func (s *Service) RecordSearch(ctx context.Context, t *core.Title, rs []core.Release) {
	if t == nil || !evidenceWindow(*t, s.now(), s.cfg().Availability) || !t.IsReleased(s.now()) {
		return
	}
	feeds := map[string]core.ReleaseFeed{}
	for _, f := range s.d.Feeds.FeedSources() {
		feeds[f.Name] = f
		if f.ID != "" {
			feeds[f.ID] = f
		}
	}
	policy := s.policy()
	for _, r := range rs {
		if ctx.Err() != nil {
			return
		}
		if r.ID == "" {
			continue
		}
		f, ok := feeds[r.Indexer]
		if !ok {
			if r.Indexer == "" {
				continue
			}
			f = core.ReleaseFeed{ID: "probe", Name: r.Indexer, Generation: "probe", Protocol: r.Protocol, Supported: true}
		}
		if err := s.remember(ctx, f, *t, r, policy); err != nil {
			return
		}
	}
}

func (s *Service) seedCached(ctx context.Context) error {
	source, ok := s.d.Feeds.(interface {
		CachedReleases(context.Context) ([]core.Release, error)
	})
	if !ok {
		return nil
	}
	rs, err := source.CachedReleases(ctx)
	if err != nil {
		return err
	}
	if len(rs) == 0 {
		return nil
	}
	for i := range rs {
		rs[i].Info = releaseparse.Parse(rs[i].Name)
	}
	feeds := map[string]core.ReleaseFeed{}
	for _, f := range s.d.Feeds.FeedSources() {
		feeds[f.Name] = f
	}
	bySource := map[string][]core.Release{}
	for _, r := range rs {
		bySource[r.Indexer] = append(bySource[r.Indexer], r)
	}
	for name, batch := range bySource {
		if f, ok := feeds[name]; ok {
			if err := s.retainFeed(ctx, f, batch); err != nil {
				return err
			}
		}
	}
	matches, err := s.d.Catalog.ReleaseMatches(ctx, rs)
	if err != nil {
		return err
	}
	// One local catalogue match pass, no search for the old library or missing items.
	for _, r := range rs {
		for _, t := range matches[r.ID] {
			s.RecordSearch(ctx, &t, []core.Release{r})
		}
	}
	return ctx.Err()
}

// Preserve unmatched names as well: an episode may enter the local catalogue
// after its upload leaves the feed's first page. Matching later is indexed and
// local; it never fetches every episode of a show.
func (s *Service) retainFeed(ctx context.Context, f core.ReleaseFeed, rs []core.Release) error {
	defer s.clearVisibility()
	for _, r := range rs {
		if r.ID == "" {
			continue
		}
		name := releaseparse.NormalizeTitle(releaseparse.Parse(r.Name).Title)
		if name == "" {
			continue
		}
		raw, err := s.encode(r)
		if err != nil {
			return err
		}
		_, err = s.db.ExecContext(ctx, `INSERT INTO availability_feed_items(id,source_id,generation,match_title,payload,seen_at,policy_digest) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id,source_id) DO UPDATE SET generation=excluded.generation,match_title=excluded.match_title,payload=CASE WHEN availability_feed_items.policy_digest=excluded.policy_digest THEN availability_feed_items.payload ELSE excluded.payload END,seen_at=excluded.seen_at,policy_digest=excluded.policy_digest`, r.ID, f.ID, f.Generation, name, raw, s.now().Unix(), releasePolicyDigest(r))
		if err != nil {
			return err
		}
	}
	return nil
}
