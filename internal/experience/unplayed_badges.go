package experience

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// pendingWatchKeys is the state capability a count read from the database
// needs: the keys whose watch writes are not stored yet.
type pendingWatchKeys interface {
	PendingKeys(userID int64) []core.RatingKey
}

// LocalUnplayedCounts is the unplayed badge of each show and season key: how
// many of the viewer's localEpisodesMany episodes they have not played. A
// key whose badge stays off (its show has more aired episodes than the local
// list holds) is missing, as are keys that are not shows or seasons.
//
// When no rule can tell one of a show's episodes from another for this
// viewer, the badge is counted, not listed: aired and played episodes per
// season in one statement per 50 series, and only the episodes the counts
// cannot decide are read and judged one by one (released inside
// availability's evidence window, or with a watch write not stored yet). A viewer with a
// rating cap or blocked genres, keywords or share filters keeps the listed
// episodes, since their rules read each episode.
func (s *Service) LocalUnplayedCounts(ctx context.Context, viewer *core.Viewer, keys []core.RatingKey) (map[core.RatingKey]int, error) {
	ctx = core.WithCatalogViewer(ctx, viewer)
	if s == nil || s.d.AiredEpisodes == nil || s.d.Visibility.SectionFor == nil {
		return nil, core.ErrNotFound
	}
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return nil, err
	}
	pending, tracked := s.d.State.(pendingWatchKeys)
	vis := s.d.Visibility
	if !tracked || s.d.AiredEpisodeTallies == nil || s.d.CachedGetMany == nil ||
		(vis.FilterAvailable != nil && s.d.EpisodesSettledBefore == nil) || !episodeRulesUniform(vis.Policy, v) {
		return s.listedUnplayedCounts(ctx, viewer, keys)
	}
	out := make(map[core.RatingKey]int, len(keys))
	shows := localShows(keys)
	if len(shows) == 0 {
		return out, nil
	}
	parents, err := s.d.CachedGetMany(ctx, shows)
	if err != nil {
		return nil, err
	}
	// Episodes inherit their series' policy; read every series once.
	vis.GetTitle = func(_ context.Context, key core.RatingKey) (*core.Title, error) {
		title, ok := parents[key]
		if !ok || title.Key != key {
			return nil, core.ErrNotFound
		}
		return &title, nil
	}
	section, ok := vis.SectionFor(core.TypeEpisode)
	sectionShown := ok && SectionAllowed(v, section)
	seriesShown := make(map[core.RatingKey]bool, len(shows))
	for _, show := range shows {
		series, err := vis.GetTitle(ctx, show)
		seriesShown[show] = err == nil && vis.TitleAllowed(v, series)
	}
	tallies, err := s.d.AiredEpisodeTallies(ctx, v.ID, shows, s.settledEpisodes(ctx), pending.PendingKeys(v.ID))
	if err != nil {
		return nil, err
	}
	unplayedJudged, err := s.judgeEpisodes(ctx, viewer, v, vis, sectionShown, tallies)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		show, ok := localShowOf(key)
		if !ok {
			continue
		}
		tally := tallies[show]
		if tally.Total > localEpisodeCap {
			continue
		}
		n := 0
		if sectionShown && seriesShown[show] {
			for season, counted := range tally.Seasons {
				if key.Kind() != core.KindSeason || season == key.Season() {
					n += counted.Aired - counted.Played
				}
			}
			for _, season := range unplayedJudged[show] {
				if key.Kind() != core.KindSeason || season == key.Season() {
					n++
				}
			}
		}
		out[key] = n
	}
	return out, nil
}

// settledEpisodes is the release time at or before which availability
// keeps every episode; zero when it holds none back.
func (s *Service) settledEpisodes(ctx context.Context) time.Time {
	if s.d.Visibility.FilterAvailable == nil {
		return time.Time{}
	}
	return s.d.EpisodesSettledBefore(ctx)
}

// judgeEpisodes reads the episodes the tallies left out and judges each as
// localEpisodesMany and UserData do, returning the seasons of the visible,
// unplayed ones by show.
func (s *Service) judgeEpisodes(ctx context.Context, viewer *core.Viewer, v *core.User, vis Visibility, sectionShown bool, tallies map[core.RatingKey]core.AiredEpisodeTally) (map[core.RatingKey][]int, error) {
	var keys []core.RatingKey
	showOf := map[core.RatingKey]core.RatingKey{}
	for show, tally := range tallies {
		if tally.Total <= localEpisodeCap {
			for _, key := range tally.Judge {
				showOf[key] = show
				keys = append(keys, key)
			}
		}
	}
	if len(keys) == 0 || !sectionShown {
		return nil, nil
	}
	titles, err := s.d.CachedGetMany(ctx, keys)
	if err != nil {
		return nil, err
	}
	candidates := make([]core.Title, 0, len(keys))
	for _, key := range keys {
		title, ok := titles[key]
		if ok && title.Key == key && title.Type == core.TypeEpisode && vis.Playable(ctx, v, &title) {
			candidates = append(candidates, title)
		}
	}
	shown := vis.Available(ctx, v, candidates)
	if len(shown) == 0 {
		return nil, nil
	}
	shownKeys := make([]core.RatingKey, len(shown))
	for i, title := range shown {
		shownKeys[i] = title.Key
	}
	states, err := s.UserData(ctx, viewer, shownKeys)
	if err != nil {
		return nil, err
	}
	out := map[core.RatingKey][]int{}
	for _, title := range shown {
		if states[title.Key].ViewCount == 0 {
			out[showOf[title.Key]] = append(out[showOf[title.Key]], title.Season)
		}
	}
	return out, nil
}

// episodeRulesUniform reports a viewer for whom every aired, non-adult
// episode of a visible series in a visible library is visible, except
// availability's own evidence window: no rating cap, and a policy that
// restricts nothing (no blocked genres or keywords, no share filters).
// Catalogue rules judge movies and series, never episodes.
func episodeRulesUniform(policy AccessPolicy, u *core.User) bool {
	if u == nil || u.MaxContentRating != "" {
		return false
	}
	if policy == nil {
		return true
	}
	p, ok := policy.(core.ArtworkAccessPolicy)
	return ok && !p.Restricted(u)
}

// listedUnplayedCounts is LocalUnplayedCounts from the listed episodes and
// their watch state.
func (s *Service) listedUnplayedCounts(ctx context.Context, viewer *core.Viewer, keys []core.RatingKey) (map[core.RatingKey]int, error) {
	lists, err := s.localEpisodesMany(ctx, viewer, keys)
	if err != nil {
		return nil, err
	}
	var episodes []core.RatingKey
	for _, key := range keys {
		if list, ok := lists[key]; ok && list.Complete {
			for _, child := range list.Items {
				episodes = append(episodes, child.Key)
			}
		}
	}
	states, err := s.UserData(ctx, viewer, episodes)
	if err != nil {
		return nil, err
	}
	out := make(map[core.RatingKey]int, len(keys))
	for _, key := range keys {
		list, ok := lists[key]
		if !ok || !list.Complete {
			continue
		}
		unplayed := 0
		for _, child := range list.Items {
			if states[child.Key].ViewCount == 0 {
				unplayed++
			}
		}
		out[key] = unplayed
	}
	return out, nil
}
