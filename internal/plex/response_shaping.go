package plex

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plex/model"
)

// pmsExcludableElements are the child elements a PMS leaves out on
// excludeElements=. Every other name (Image, UltraBlurColors, Collection,
// Label, Extras, Related, Field, Chapter…) is ignored, as an unknown name is.
// Part takes its Streams with it and leaves Media@partCount; Media leaves
// mediaCount.
var pmsExcludableElements = map[string]bool{
	"Media": true, "Part": true, "Genre": true, "Country": true, "Guid": true,
	"Rating": true, "Director": true, "Writer": true, "Role": true, "Producer": true,
}

// queryNames is the union of a comma-separated query option's values.
func queryNames(q url.Values, key string) map[string]bool {
	var out map[string]bool
	for _, raw := range q[key] {
		for _, name := range strings.Split(raw, ",") {
			if name = strings.TrimSpace(name); name != "" {
				if out == nil {
					out = map[string]bool{}
				}
				out[name] = true
			}
		}
	}
	return out
}

// responseExclusions are the excludeFields= and (PMS's allow-listed)
// excludeElements= names a library response honours. A PMS applies both to
// every container that carries rows, its own attributes included, except a
// transcode decision. Live TV providers and the playlist and play-queue
// family (encoded with their own long-standing rule) are left alone.
func responseExclusions(r *http.Request) (fields, elements map[string]bool) {
	p := r.URL.Path
	if strings.HasSuffix(p, "/decision") || strings.HasPrefix(p, "/playlists") || strings.HasPrefix(p, "/playQueues") ||
		strings.HasPrefix(p, "/livetv") || strings.HasPrefix(p, "/tv.plex.providers") {
		return nil, nil
	}
	q := r.URL.Query()
	fields = queryNames(q, "excludeFields")
	for name := range queryNames(q, "excludeElements") {
		if pmsExcludableElements[name] {
			if elements == nil {
				elements = map[string]bool{}
			}
			elements[name] = true
		}
	}
	return fields, elements
}

// listMediaSkip lets renderTitles skip building Media a response excludes.
// Tests turn it off to prove the skip answers the same bytes.
var listMediaSkip = true

// excludesListMedia reports a request whose rows will be answered without
// Media, so listing rows need not build it (renderTitles).
func excludesListMedia(r *http.Request) bool {
	_, elements := responseExclusions(r)
	return elements["Media"]
}

// substituteExcludedCounts gives every row the counts PMS sends in place of
// excluded children: mediaCount for Media, Media@partCount for Part. It
// reaches rows nested in hubs, search results and inline blocks.
func substituteExcludedCounts(mc *model.MediaContainer, elements map[string]bool) {
	media, part := elements["Media"], elements["Part"]
	if !media && !part {
		return
	}
	var row func(*model.Metadata)
	rows := func(ms []model.Metadata) {
		for i := range ms {
			row(&ms[i])
		}
	}
	row = func(m *model.Metadata) {
		switch {
		case media:
			if m.MediaCount == nil {
				// As applyExcludedElements: a playable row whose Media
				// was not built still has its one version.
				count := len(m.Media)
				if count == 0 && isPlayable(core.MediaType(m.Type)) {
					count = 1
				}
				if count > 0 {
					m.MediaCount = model.Ptr(count)
					m.MediaCountOptimized = model.Ptr(0)
				}
			}
		case part:
			for j := range m.Media {
				if m.Media[j].PartCount == nil {
					m.Media[j].PartCount = model.Ptr(len(m.Media[j].Part))
				}
			}
		}
		if m.OnDeck != nil {
			row(&m.OnDeck.Metadata)
		}
		if m.Children != nil {
			rows(m.Children.Metadata)
		}
		if m.Extras != nil {
			rows(m.Extras.Metadata)
		}
		if m.Related != nil {
			for h := range m.Related.Hub {
				rows(m.Related.Hub[h].Metadata)
			}
		}
	}
	rows(mc.Metadata)
	for h := range mc.Hub {
		rows(mc.Hub[h].Metadata)
	}
	for i := range mc.SearchResult {
		if mc.SearchResult[i].Metadata != nil {
			row(mc.SearchResult[i].Metadata)
		}
	}
}
