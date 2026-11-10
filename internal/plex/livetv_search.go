package plex

import (
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plex/model"
)

// handleLiveChannelSearch is deliberately channel-only. Programme titles and
// provider URLs never participate, and the ordinary channel policy determines
// visibility before any result is ranked or counted.
func (s *Server) handleLiveChannelSearch(w http.ResponseWriter, r *http.Request) {
	limit, ok := searchLimit(w, r, 10, 50)
	if !ok {
		return
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("query")))
	if utf8.RuneCountInString(query) > 256 {
		writeHTTPError(w, http.StatusBadRequest)
		return
	}
	mc := &model.MediaContainer{Identifier: frontend.LiveTVProvider}
	if query == "" {
		s.write(w, r, mc)
		return
	}
	channels, err := s.d.LiveTV.AllowedChannels(r.Context(), FromContext(r.Context()).User)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	terms := strings.Fields(query)
	for _, c := range channels {
		name := strings.ToLower(c.Name)
		number := strings.ToLower(c.Number)
		match := true
		for _, term := range terms {
			if !strings.Contains(name, term) && !strings.Contains(number, term) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		score := 0.75
		if name == query || number == query {
			score = 1
		} else if strings.HasPrefix(name, query) {
			score = 0.9
		}
		// Keep at most limit hits even for a large provider lineup. Equal scores
		// preserve channel order; exact matches can displace weaker matches.
		if len(mc.SearchResult) == limit && score <= mc.SearchResult[limit-1].Score {
			continue
		}
		categories := model.StringList{}
		if c.Group != "" {
			categories = append(categories, c.Group)
		}
		ch := &model.SearchChannel{ID: c.ID, GridKey: c.ID, VCN: c.Number, Title: c.Name, ShortTitle: c.Name, Categories: categories,
			Source: "server://" + s.identity().MachineIdentifier + "/" + frontend.LiveTVProvider}
		mc.SearchResult = append(mc.SearchResult, model.SearchResult{Score: score, Channel: ch})
		sort.SliceStable(mc.SearchResult, func(i, j int) bool { return mc.SearchResult[i].Score > mc.SearchResult[j].Score })
		if len(mc.SearchResult) > limit {
			mc.SearchResult = mc.SearchResult[:limit]
		}
	}
	mc.Size = len(mc.SearchResult)
	s.write(w, r, mc)
}
