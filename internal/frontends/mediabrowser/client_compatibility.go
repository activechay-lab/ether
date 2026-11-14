package mediabrowser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
		{"/System/WakeOnLanInfo", i.emptyOptionalArray},
		{"/Users/{userId}/HomeSections", i.homeSections},
		{"/Artists", i.emptyOptionalItems}, {"/Artists/AlbumArtists", i.emptyOptionalItems},
		{"/Items/{id}/InstantMix", i.emptyItemCompanions},
		{"/LiveTv/Timers", i.emptyOptionalItems}, {"/LiveTv/SeriesTimers", i.emptyOptionalItems},
		{"/LiveTv/Recordings/Folders", i.emptyOptionalItems},
		{"/Years", i.years},
		{"/Years/{year}", i.year},
		{"/UserItems/{id}/UserData", i.userItemData},
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
	// A cold aggregate or unavailable index must not hold a library page.
	// All five indexed lookups share one small request budget.
	ctx, cancel := context.WithTimeout(r.Context(), 250*time.Millisecond)
	defer cancel()
	// Read each facet independently: an absent optional facet must not hide
	// the filters that are available. Production implements the indexed reader;
	// embedded hosts can still supply their bounded Facets implementation.
	read := func(kind string) []frontend.FacetValue {
		var values []frontend.FacetValue
		var err error
		if reader, ok := i.host.Experience.(frontend.FilterFacetReader); ok {
			values, err = reader.FilterFacets(ctx, v, device(r), facetView(r), kind)
		} else {
			values, err = i.host.Experience.Facets(ctx, v, device(r), facetView(r), kind)
		}
		if err != nil {
			i.logFacetFailure(kind, err)
			return nil
		}
		return values
	}
	genres, studios := read("genre"), read("studio")
	genreNames, studioNames, tags, ratings := []string{}, []string{}, []string{}, []string{}
	genreItems, studioItems := []any{}, []any{}
	for _, value := range genres {
		genreNames = append(genreNames, value.Title)
		genreItems = append(genreItems, map[string]any{"Name": value.Title, "Id": value.ID})
	}
	for _, value := range studios {
		studioNames = append(studioNames, value.Title)
		studioItems = append(studioItems, map[string]any{"Name": value.Title, "Id": value.ID})
	}
	for _, value := range read("tag") {
		tags = append(tags, value.Title)
	}
	for _, value := range read("contentRating") {
		ratings = append(ratings, contentrating.Prefixed(value.Title))
	}
	years := []int{}
	for _, value := range read("year") {
		if n, err := strconv.Atoi(value.Title); err == nil {
			years = append(years, n)
		}
	}
	result := map[string]any{"Genres": genreNames, "Studios": studioNames, "Tags": tags, "OfficialRatings": ratings, "Years": years}
	if strings.HasSuffix(strings.ToLower(r.URL.Path), "/items/filters2") {
		result["Genres"], result["Studios"] = genreItems, studioItems
		result["AudioLanguages"], result["SubtitleLanguages"] = []any{}, []any{}
	}
	jsonReply(w, result)
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

// homeSections is the Emby home layout the native apps request at startup
// (GET /Users/{userId}/HomeSections, Emby Server 4.10+). Ether has no
// per-user section editor, so it answers the standard layout: library
// tiles, Continue Watching, Next Up, then Latest Media per visible library.
// Known section types carry no DisplayText; the apps label them with their
// own localized strings. The viewer scope comes from i.viewer, which also
// rejects a {userId} that is not the caller.
func (i *instance) homeSections(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil || v == nil {
		http.Error(w, "Unauthorized", 401)
		return
	}
	if i.host.Experience == nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	views, err := i.host.Experience.Views(r.Context(), v)
	if err != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	items := []any{}
	add := func(sectionType, collectionID string) {
		section := map[string]any{"Id": homeSectionID(sectionType, collectionID), "SectionType": sectionType}
		if collectionID != "" {
			section["CollectionId"] = collectionID
		}
		items = append(items, section)
	}
	add("SmallLibraryTiles", "")
	add("Resume", "")
	add("NextUp", "")
	for _, view := range views {
		if view.Type != "movie" && view.Type != "show" {
			continue
		}
		n, err := ids.ParseDecimal(view.ID)
		if err != nil {
			continue
		}
		id, err := ids.GUID(ids.View, n)
		if err != nil {
			continue
		}
		add("LatestMedia", id)
	}
	jsonReply(w, i.queryResult(items, len(items), 0))
}

// homeSectionID is a stable GUID-shaped id for one standard section. It is
// not an item id; nothing in Ether resolves it.
func homeSectionID(sectionType, collectionID string) string {
	sum := sha256.Sum256([]byte("ether.homesection." + sectionType + "." + collectionID))
	return hex.EncodeToString(sum[:16])
}

func (i *instance) emptyOptionalArray(w http.ResponseWriter, r *http.Request) {
	if v, _, err := i.viewer(r); err != nil || v == nil {
		http.Error(w, "Unauthorized", 401)
		return
	}
	jsonReply(w, []any{})
}

// years lists the production years a filter can offer, from the same year
// facet /Items/Filters reads. Jellyfin Web and Swiftfin ask GET /Years to build
// their year filter; it must exist, not only the embedded Filters list.
func (i *instance) years(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil || v == nil {
		http.Error(w, "Unauthorized", 401)
		return
	}
	if i.host.Experience == nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	facets, err := i.host.Experience.Facets(r.Context(), v, device(r), facetView(r), "year")
	if err != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	offset, limit := queryInt(r, "StartIndex", 0), queryInt(r, "Limit", 50)
	if limit <= 0 {
		limit = 50
	}
	items := make([]any, 0, len(facets))
	for _, facet := range facets {
		n, e := strconv.Atoi(facet.Title)
		if e != nil {
			continue
		}
		id, e := ids.GUID(ids.View, int64(n))
		if e != nil {
			continue
		}
		items = append(items, map[string]any{"Name": facet.Title, "Id": id, "Type": "Year", "ServerId": i.info.ID, "ProductionYear": n})
	}
	start := min(max(offset, 0), len(items))
	end := min(start+limit, len(items))
	jsonReply(w, i.queryResult(items[start:end], len(items), offset))
}

// year answers GET /Years/{year} with the year item a client opened. Its items
// are reachable through /Items?Years=, which clients use for the listing.
func (i *instance) year(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil || v == nil {
		http.Error(w, "Unauthorized", 401)
		return
	}
	raw := r.PathValue("year")
	n, err := strconv.Atoi(raw)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	id, err := ids.GUID(ids.View, int64(n))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	jsonReply(w, map[string]any{"Name": raw, "Id": id, "Type": "Year", "ServerId": i.info.ID, "ProductionYear": n, "IsFolder": true})
}

// userItemData answers GET /UserItems/{id}/UserData, which Jellyfin Web reads
// for an item's played, favorite and resume state outside a full detail load.
func (i *instance) userItemData(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil || v == nil {
		http.Error(w, "Unauthorized", 401)
		return
	}
	if i.host.Experience == nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	key, err := ids.ParseRatingKeyGUID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := i.host.Experience.Item(i.identityContext(r), frontend.ItemRequest{Viewer: v, Device: device(r), Key: key}); err != nil {
		http.NotFound(w, r)
		return
	}
	idsWire := ids.RatingKeyGUID(key)
	state, err := i.host.Experience.UserData(i.identityContext(r), v, []frontend.RatingKey{key})
	if err != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	jsonReply(w, i.userDataDTO(idsWire, state[key]))
}
