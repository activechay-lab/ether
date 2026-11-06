package mediabrowser

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontend/kit/route"
	kitsort "github.com/braedonsaunders/ether/internal/frontend/kit/sort"
)

func (i *instance) clientCompatibilityRoutes() error {
	for _, entry := range []struct {
		path    string
		handler http.HandlerFunc
	}{
		{"/Items/Suggestions", i.suggestions},
		{"/Users/{userId}/Items/Suggestions", i.suggestions},
		{"/Items/Filters", i.queryFilters}, {"/Items/Filters2", i.queryFilters},
		{"/Shows/Upcoming", i.upcomingEpisodes},
		{"/Videos/{id}/AdditionalParts", i.additionalParts},
		{"/Items/{id}/ThemeMedia", i.themeMedia}, {"/Items/{id}/ThemeSongs", i.themeMedia}, {"/Items/{id}/ThemeVideos", i.themeMedia},
		{"/Items/{id}/PlaybackInfo", i.playbackInfo},
		{"/Localization/Options", i.emptyOptionalArray},
		{"/Artists", i.emptyOptionalItems}, {"/Artists/AlbumArtists", i.emptyOptionalItems},
		{"/Items/{id}/InstantMix", i.emptyItemCompanions},
		{"/LiveTv/Timers", i.emptyOptionalItems}, {"/LiveTv/SeriesTimers", i.emptyOptionalItems},
		{"/LiveTv/Recordings/Folders", i.emptyOptionalItems},
	} {
		if err := i.router.Handle("GET", entry.path, entry.handler); err != nil {
			return err
		}
	}
	return i.router.Handle("DELETE", "/Videos/ActiveEncodings", http.HandlerFunc(i.stopEncoding))
}

func (i *instance) queryFilters(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil || v == nil {
		http.Error(w, "Unauthorized", 401)
		return
	}
	if i.host.Experience == nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	// Facets are derived from the viewer's visible library, never the global index.
	genres, err := i.host.Experience.Facets(r.Context(), v, device(r), facetView(r), "genre")
	if err != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	if strings.EqualFold(r.URL.Path, "/Items/Filters2") {
		names := []any{}
		for _, g := range genres {
			names = append(names, map[string]any{"Name": g.Title, "Id": g.ID})
		}
		jsonReply(w, map[string]any{"Genres": names, "Tags": []string{}, "AudioLanguages": []any{}, "SubtitleLanguages": []any{}})
	} else {
		names := []string{}
		for _, g := range genres {
			names = append(names, g.Title)
		}
		years, err := i.host.Experience.Facets(r.Context(), v, device(r), facetView(r), "year")
		if err != nil {
			http.Error(w, "Unavailable", 503)
			return
		}
		ratings, err := i.host.Experience.Facets(r.Context(), v, device(r), facetView(r), "contentRating")
		if err != nil {
			http.Error(w, "Unavailable", 503)
			return
		}
		yearValues := []int{}
		for _, year := range years {
			if n, e := strconv.Atoi(year.Title); e == nil {
				yearValues = append(yearValues, n)
			}
		}
		ratingValues := []string{}
		for _, rating := range ratings {
			ratingValues = append(ratingValues, contentrating.Prefixed(rating.Title))
		}
		jsonReply(w, map[string]any{"Genres": names, "Tags": []string{}, "OfficialRatings": ratingValues, "Years": yearValues})
	}
}

func (i *instance) upcomingEpisodes(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil || v == nil {
		http.Error(w, "Unauthorized", 401)
		return
	}
	if i.host.Experience == nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	q := itemQuery(r)
	q.Types = []frontend.MediaType{"episode"}
	q.Recursive = true
	q.Sort, _ = kitsort.Order("PremiereDate")
	q.Desc = false
	now := time.Now().UTC().Truncate(24 * time.Hour)
	q.Filter.ReleasedFilters = []frontend.DateFilter{{Op: ">=", Unix: now.AddDate(0, 0, -1).Unix()}}
	if len(route.Query(r.URL.Query(), "Limit")) == 0 {
		q.Limit = showChildPageMax
	}
	view := ""
	if parent := route.Query(r.URL.Query(), "parentId"); len(parent) > 0 {
		if len(parent) != 1 {
			http.Error(w, "Bad Request", 400)
			return
		}
		n, e := ids.ParseGUID(ids.View, parent[0])
		if e != nil {
			http.NotFound(w, r)
			return
		}
		view = ids.Decimal(n)
	}
	page, err := i.host.Experience.LibraryItems(r.Context(), v, device(r), view, q)
	if err != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	i.page(w, r, v, page, q.Offset)
}

func (i *instance) additionalParts(w http.ResponseWriter, r *http.Request) {
	if _, ok := i.authorizedItem(w, r); !ok {
		return
	}
	// Ether sources are complete editions, with no stacked additional files.
	jsonReply(w, i.queryResult([]any{}, 0, 0))
}

func (i *instance) themeMedia(w http.ResponseWriter, r *http.Request) {
	if _, ok := i.authorizedItem(w, r); !ok {
		return
	}
	// Theme assets are optional and Ether does not index them. Preserve the
	// nested result shape expected by Jellyfin Web rather than a bare array.
	empty := i.queryResult([]any{}, 0, 0)
	empty["OwnerId"] = "00000000000000000000000000000000"
	if strings.EqualFold(r.URL.Path, "/Items/"+r.PathValue("id")+"/ThemeMedia") {
		jsonReply(w, map[string]any{"ThemeSongsResult": empty, "ThemeVideosResult": empty, "SoundtrackSongsResult": empty})
		return
	}
	jsonReply(w, empty)
}

// Stop only the authenticated device's broker session; a client-supplied
// deviceId must never select another viewer's encoder.
func (i *instance) stopEncoding(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil || v == nil {
		http.Error(w, "Unauthorized", 401)
		return
	}
	dev := device(r)
	devices := route.Query(r.URL.Query(), "deviceId")
	sessions := route.Query(r.URL.Query(), "playSessionId")
	if len(devices) != 1 || devices[0] == "" || len(sessions) != 1 || sessions[0] == "" {
		http.Error(w, "Bad Request", 400)
		return
	}
	if dev.ID == "" || devices[0] != dev.ID {
		http.Error(w, "Forbidden", 403)
		return
	}
	if i.host.Sessions == nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	err = i.host.Sessions.Stop(r.Context(), frontend.SessionAccess{Viewer: v, Device: dev, Protocol: i.protocol}, sessions[0])
	if err != nil && !errors.Is(err, frontend.ErrNotFound) {
		http.Error(w, "Unavailable", 503)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// No music libraries or DVR scheduler are advertised by this surface. These
// optional collection reads still have the standard empty query-result shape.
func (i *instance) emptyOptionalItems(w http.ResponseWriter, r *http.Request) {
	if v, _, err := i.viewer(r); err != nil || v == nil {
		http.Error(w, "Unauthorized", 401)
		return
	}
	jsonReply(w, i.queryResult([]any{}, 0, queryInt(r, "StartIndex", 0)))
}
func (i *instance) emptyItemCompanions(w http.ResponseWriter, r *http.Request) {
	if _, ok := i.authorizedItem(w, r); !ok {
		return
	}
	jsonReply(w, i.queryResult([]any{}, 0, 0))
}

func (i *instance) emptyOptionalArray(w http.ResponseWriter, r *http.Request) {
	if v, _, err := i.viewer(r); err != nil || v == nil {
		http.Error(w, "Unauthorized", 401)
		return
	}
	jsonReply(w, []any{})
}
