package experience

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// LocalEpisodesMany is LocalEpisodes for a page of shows and seasons. Every
// show's aired episodes come from one batched read, the series for inherited
// policy from one more, and availability is judged in one pass, so a list
// of series costs the same few statements however many it holds. Keys that
// are not shows or seasons are left out.
func (s *Service) LocalEpisodesMany(ctx context.Context, viewer *core.Viewer, keys []core.RatingKey) (map[core.RatingKey]frontend.LocalEpisodeList, error) {
	ctx = core.WithCatalogViewer(ctx, viewer)
	if s == nil || s.d.AiredEpisodes == nil || s.d.Visibility.SectionFor == nil {
		return nil, core.ErrNotFound
	}
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return nil, err
	}
	showOf := func(key core.RatingKey) (core.RatingKey, bool) {
		switch key.Kind() {
		case core.KindShow:
			return key, true
		case core.KindSeason:
			return core.ShowKey(key.TMDB()), true
		}
		return 0, false
	}
	var shows []core.RatingKey
	seen := map[core.RatingKey]bool{}
	for _, key := range keys {
		if show, ok := showOf(key); ok && !seen[show] {
			seen[show] = true
			shows = append(shows, show)
		}
	}
	out := make(map[core.RatingKey]frontend.LocalEpisodeList, len(keys))
	if len(shows) == 0 {
		return out, nil
	}
	pages, err := s.airedShowEpisodesMany(ctx, shows)
	if err != nil {
		return nil, err
	}
	vis := s.d.Visibility
	if s.d.CachedGetMany != nil {
		// Episodes inherit their series' policy; read every series once.
		parents, err := s.d.CachedGetMany(ctx, shows)
		if err != nil {
			return nil, err
		}
		vis.GetTitle = func(_ context.Context, key core.RatingKey) (*core.Title, error) {
			title, ok := parents[key]
			if !ok || title.Key != key {
				return nil, core.ErrNotFound
			}
			return &title, nil
		}
	}
	// The checks s.visible makes per episode, with availability judged for
	// all of them at once.
	var candidates []core.Title
	for _, show := range shows {
		for _, title := range pages[show].Items {
			if title.Type != core.TypeEpisode {
				continue
			}
			section, ok := vis.SectionFor(title.Type)
			if ok && SectionAllowed(v, section) && vis.Playable(ctx, v, &title) {
				candidates = append(candidates, title)
			}
		}
	}
	shown := make(map[core.RatingKey]bool, len(candidates))
	for _, title := range vis.Available(ctx, v, candidates) {
		shown[title.Key] = true
	}
	for _, key := range keys {
		show, ok := showOf(key)
		if !ok {
			continue
		}
		page := pages[show]
		list := frontend.LocalEpisodeList{Items: make([]core.Title, 0, len(page.Items)), Complete: page.Total == len(page.Items)}
		for _, title := range page.Items {
			if key.Kind() == core.KindSeason && title.Season != key.Season() {
				continue
			}
			if shown[title.Key] {
				list.Items = append(list.Items, title)
			}
		}
		out[key] = list
	}
	return out, nil
}

// airedShowEpisodesMany reads at most 500 stored episodes per show. A
// seasons page asks once per season; the request memo keeps each show to a
// single read, and the shows it has not seen are read in one batch.
func (s *Service) airedShowEpisodesMany(ctx context.Context, shows []core.RatingKey) (map[core.RatingKey]core.Page, error) {
	out := make(map[core.RatingKey]core.Page, len(shows))
	memo := frontend.RequestMemoFrom(ctx)
	var missing []core.RatingKey
	if memo != nil {
		memo.Mu.Lock()
		for _, show := range shows {
			if hit, ok := memo.Aired[show]; ok {
				if hit.Err != nil {
					memo.Mu.Unlock()
					return nil, hit.Err
				}
				out[show] = core.Page{Items: append([]core.Title(nil), hit.Items...), Total: hit.Total}
				continue
			}
			missing = append(missing, show)
		}
		memo.Mu.Unlock()
	} else {
		missing = shows
	}
	if len(missing) == 0 {
		return out, nil
	}
	var read map[core.RatingKey]core.Page
	var err error
	if s.d.AiredEpisodesMany != nil && len(missing) > 1 {
		read, err = s.d.AiredEpisodesMany(ctx, missing, 500)
	} else {
		read = make(map[core.RatingKey]core.Page, len(missing))
		for _, show := range missing {
			page, e := s.d.AiredEpisodes(ctx, show, 0, 500)
			if e != nil {
				err = e
				break
			}
			read[show] = page
		}
	}
	if err != nil {
		return nil, err
	}
	if memo != nil {
		memo.Mu.Lock()
		if memo.Aired == nil {
			memo.Aired = map[core.RatingKey]frontend.AiredMemo{}
		}
		for _, show := range missing {
			if _, ok := memo.Aired[show]; !ok {
				page := read[show]
				memo.Aired[show] = frontend.AiredMemo{Items: append([]core.Title(nil), page.Items...), Total: page.Total}
			}
		}
		memo.Mu.Unlock()
	}
	for _, show := range missing {
		out[show] = read[show]
	}
	return out, nil
}
