package stremio

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

func (i *instance) meta(w http.ResponseWriter, r *http.Request) {
	credential := r.PathValue("credential")
	viewer, err := i.host.Identity.Resolve(r.Context(), credential)
	if err != nil || viewer == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	i.observeClient(r, credential, viewer)
	typ, raw := r.PathValue("type"), r.PathValue("id")
	if !strings.HasSuffix(raw, ".json") {
		http.NotFound(w, r)
		return
	}
	rawID := strings.TrimSuffix(raw, ".json")
	device := deviceFor(credential)
	if frontend.ItemRef(rawID).Valid() {
		i.metaPluginItem(w, r, credential, viewer, device, typ, rawID)
		return
	}
	id, err := ids.ParseStremio(rawID)
	if err != nil || id.HasEpisode {
		http.NotFound(w, r)
		return
	}
	key, err := i.resolveID(r, viewer, device, typ, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	detail, err := i.host.Experience.Item(r.Context(), frontend.ItemRequest{Viewer: viewer, Device: device, Key: key})
	if err != nil {
		http.NotFound(w, r)
		return
	}
	title := detail.Title
	meta := map[string]any{"id": strings.TrimSuffix(raw, ".json"), "type": typ, "name": title.Title, "description": stremioDescription(r, i.host.Experience, viewer, title), "genres": title.Genres}
	if title.Year > 0 {
		meta["releaseInfo"] = strconv.Itoa(title.Year)
	}
	if poster := i.artwork(r, credential, viewer, device, []frontend.Title{title})[title.Key]; poster != "" {
		meta["poster"] = poster
	}
	if typ == "series" {
		videos := make([]map[string]any, 0)
		for offset := 0; ; {
			page, e := i.host.Experience.Children(r.Context(), viewer, device, key, frontend.ItemQuery{Offset: offset, Limit: 500, Recursive: true})
			if e != nil {
				http.Error(w, "Unavailable", http.StatusServiceUnavailable)
				return
			}
			for _, episode := range page.Items {
				if string(episode.Type) != "episode" {
					continue
				}
				// The displayed episode order can be virtual. Stremio identifiers
				// must still resolve to the canonical catalogue episode key.
				canonical := ids.StremioID{Source: "tmdb", ID: strconv.FormatInt(key.TMDB(), 10), HasEpisode: true, Season: episode.Key.Season(), Episode: episode.Key.Episode()}
				resolved, e := ids.StremioTMDBKey("series", canonical)
				if e != nil || resolved != episode.Key {
					continue
				}
				episodeID, e := ids.FormatStremio(canonical)
				if e != nil {
					continue
				}
				videos = append(videos, map[string]any{"id": episodeID, "title": episode.Title, "season": episode.Season, "episode": episode.Episode, "overview": episode.Summary})
			}
			offset += len(page.Items)
			if len(page.Items) == 0 || offset >= page.Total {
				break
			}
		}
		meta["videos"] = videos
	}
	writeJSON(w, map[string]any{"meta": meta})
}

// metaPluginItem answers a pk: ItemRef meta request (spec §6.4). The ItemRef
// resolves to the stored item's RatingKey through the host seam; children of a
// series presentation are addressed by their own pk: ItemRefs.
func (i *instance) metaPluginItem(w http.ResponseWriter, r *http.Request, credential string, viewer *frontend.Viewer, device frontend.Device, typ, rawID string) {
	refs, ok := i.host.KindStreams.(frontend.KindRefKeys)
	if !ok {
		http.NotFound(w, r)
		return
	}
	key, err := refs.KeyForRef(r.Context(), viewer, frontend.ItemRef(rawID))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	detail, err := i.host.Experience.Item(r.Context(), frontend.ItemRequest{Viewer: viewer, Device: device, Key: key})
	if err != nil {
		http.NotFound(w, r)
		return
	}
	title := detail.Title
	meta := map[string]any{"id": rawID, "type": typ, "name": title.Title, "description": stremioDescription(r, i.host.Experience, viewer, title), "genres": title.Genres}
	if title.Year > 0 {
		meta["releaseInfo"] = strconv.Itoa(title.Year)
	}
	if poster := i.artwork(r, credential, viewer, device, []frontend.Title{title})[title.Key]; poster != "" {
		meta["poster"] = poster
	}
	if typ == "series" {
		videos := make([]map[string]any, 0)
		for offset := 0; ; {
			page, e := i.host.Experience.Children(r.Context(), viewer, device, key, frontend.ItemQuery{Offset: offset, Limit: 500, Recursive: true})
			if e != nil {
				http.Error(w, "Unavailable", http.StatusServiceUnavailable)
				return
			}
			keys := make([]frontend.RatingKey, 0, len(page.Items))
			for _, child := range page.Items {
				keys = append(keys, child.Key)
			}
			childRefs, _ := refs.RefsForKeys(r.Context(), viewer, keys)
			for _, child := range page.Items {
				childRef := childRefs[child.Key]
				if childRef == "" {
					continue
				}
				videos = append(videos, map[string]any{"id": string(childRef), "title": child.Title, "season": child.Season, "episode": child.Episode, "overview": child.Summary})
			}
			offset += len(page.Items)
			if len(page.Items) == 0 || offset >= page.Total {
				break
			}
		}
		meta["videos"] = videos
	}
	writeJSON(w, map[string]any{"meta": meta})
}

// stremioDescription puts the match header at the start of the only prose
// field Stremio renders, and names provider ratings there. Stremio's
// imdbRating field would mislabel a non-IMDb score, so it stays unused.
func stremioDescription(r *http.Request, exp frontend.Experience, viewer *frontend.Viewer, title frontend.Title) string {
	description := title.Summary
	source, ok := exp.(frontend.DetailFactsSource)
	if !ok || r == nil || viewer == nil {
		return description
	}
	facts := source.DetailFacts(r.Context(), viewer, title)
	if facts.Header != "" {
		if strings.TrimSpace(description) == "" {
			description = facts.Header
		} else {
			description = facts.Header + "\n\n" + description
		}
	}
	if !facts.RatingsKnown || len(facts.Sources) == 0 {
		return description
	}
	parts := make([]string, 0, len(facts.Sources))
	for _, rating := range facts.Sources {
		if rating.Name != "" && rating.Display != "" {
			parts = append(parts, rating.Name+" "+rating.Display)
		}
	}
	if len(parts) == 0 {
		return description
	}
	line := strings.Join(parts, " · ")
	if strings.TrimSpace(description) == "" {
		return line
	}
	return description + "\n\n" + line
}
