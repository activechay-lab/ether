package plex

import (
	"context"
	"github.com/braedonsaunders/ether/internal/frontend"
	"net/url"
	"strconv"
)

// recentlyWatchedHub shares the listing parser and indexed query with See All,
// so a resumed rewatch cannot remain in the shelf after leaving its key listing.
func (s *Server) recentlyWatchedHub(ctx context.Context, pr *Request, sec Section, count int) (hubSpec, error) {
	kind := sec.Type
	prefix, title := "movie", "Recently Watched Movies"
	if kind == frontend.TypeShow {
		kind = frontend.TypeEpisode
		prefix, title = "tv", "Recently Watched Episodes"
	}
	q := url.Values{"sort": {"lastViewedAt:desc"}, "unwatched": {"0"}, "viewOffset": {"0"}}
	if kind == frontend.TypeEpisode {
		q.Set("type", "4")
	}
	br := s.parseBrowse(ctx, sec, kind, pr.User, q, q.Encode())
	page := *pr
	page.HasPage = true
	page.Start = 0
	page.Size = min(max(count, 0)+1, 201)
	items, total, _, err := s.browse(ctx, &page, br)
	if err != nil {
		return hubSpec{}, err
	}
	more := len(items) > count
	if more {
		items = items[:max(count, 0)]
	}
	id := strconv.Itoa(sec.ID)
	return hubSpec{Hub: frontend.Hub{ID: prefix + ".recentlyviewed." + id, Context: "hub." + prefix + ".recentlyviewed", Title: title, Type: kind, Style: "shelf", Items: items, More: more || total > len(items)}, key: "/library/sections/" + id + "/all?" + q.Encode(), noPromoted: true}, nil
}
