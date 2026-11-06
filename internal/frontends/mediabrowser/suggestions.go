package mediabrowser

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/paging"
	"github.com/braedonsaunders/ether/internal/frontend/kit/route"
)

// suggestions projects the same authorized home recommendations used by the
// personal libraries. Read fresh rows so a policy change cannot reuse stale
// viewer recommendations. Experience applies viewer visibility and safety.
func (i *instance) suggestions(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil || v == nil {
		http.Error(w, "Unauthorized", 401)
		return
	}
	offset, limit := 0, -1
	for name, target := range map[string]*int{"startIndex": &offset, "limit": &limit} {
		values := route.Query(r.URL.Query(), name)
		if len(values) == 0 {
			continue
		}
		if len(values) != 1 {
			http.Error(w, "Bad Request", 400)
			return
		}
		n, e := strconv.ParseInt(values[0], 10, 32)
		if e != nil || n < 0 {
			http.Error(w, "Bad Request", 400)
			return
		}
		*target = int(n)
	}
	count := false
	if values := route.Query(r.URL.Query(), "enableTotalRecordCount"); len(values) > 0 {
		if len(values) != 1 {
			http.Error(w, "Bad Request", 400)
			return
		}
		count, err = strconv.ParseBool(values[0])
		if err != nil {
			http.Error(w, "Bad Request", 400)
			return
		}
	}
	types, media := suggestionValues(r, "type"), suggestionValues(r, "mediaType")
	if i.host.Experience == nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	rows, err := i.personalHomeRows(r.Context(), v, device(r))
	if err != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	titles := []frontend.Title{}
	seen := map[frontend.RatingKey]bool{}
	wireTypes := map[frontend.MediaType]string{"movie": "movie", "show": "series", "season": "season", "episode": "episode"}
	for _, row := range rows {
		if !personalRowAllowed(personalHomeConfig{}, row) {
			continue
		}
		for _, title := range personalTitles(row) {
			if seen[title.Key] {
				continue
			}
			typ := wireTypes[title.Type]
			if typ == "" || len(types) > 0 && !types[typ] {
				continue
			}
			mt := "unknown"
			if title.Type == "movie" || title.Type == "episode" {
				mt = "video"
			}
			if len(media) > 0 && !media[mt] {
				continue
			}
			seen[title.Key] = true
			titles = append(titles, title)
		}
	}
	total := 0
	if count {
		total = len(titles)
	}
	i.pageWithCount(w, r, v, frontend.Page{Items: paging.Slice(titles, offset, limit), Total: total}, offset, 0, "", &count)
}

func suggestionValues(r *http.Request, name string) map[string]bool {
	out := map[string]bool{}
	for _, value := range route.Query(r.URL.Query(), name) {
		for _, part := range strings.Split(value, ",") {
			if part = strings.ToLower(strings.TrimSpace(part)); part != "" {
				out[part] = true
			}
		}
	}
	return out
}
