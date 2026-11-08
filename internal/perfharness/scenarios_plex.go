package perfharness

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// classState is a watch-state write a player sends during and after
// playback (timeline, progress, scrobble, rating, play queues). It is
// gated like a page: a write that scales with the library or the viewer's
// history stalls every player in the house.
const classState = "state"

// plexLibrarySteps are the Plex paths a viewer reaches past the first page
// of a library: filters by facet and by person, person pages, franchises,
// deep pages, hub "more" pages and playlists, with the parameters real
// clients send (internal/conform/plex, testdata/conform/plex).
func plexLibrarySteps(page string) []Step {
	all := "/library/sections/{movieSection}/all?type=1&sort=titleSort&X-Plex-Container-Start=0" + page
	return []Step{
		// A famous title's detail names the people and facets the filters
		// below follow, as a viewer tapping a cast member or a genre does.
		{Name: "movies all popularity", Class: classBrowse, Path: "/library/sections/{movieSection}/all?type=1&sort=popularity:desc&X-Plex-Container-Start=0" + page, Discover: discoverPlexItem("popularMovieKey", "movie")},
		{Name: "popular movie detail", Class: classDetail, QueriesBound: detailQueriesBound, Path: "/library/metadata/{popularMovieKey}?includeExtras=1&includeRelated=1&includeOnDeck=1&includeChapters=1&includeGuids=1&includeMarkers=1&includeConcerts=1&includePreferences=1&includeReviews=1&asyncAugmentMetadata=1", Discover: discoverPlexTags},
		{Name: "movies filter genre", Class: classBrowse, Path: all + "&{genreFilter}"},
		{Name: "movies filter genre deep", Class: classBrowse, Path: "/library/sections/{movieSection}/all?type=1&sort=titleSort&{genreFilter}&X-Plex-Container-Start=1200" + page},
		{Name: "movies filter genre by rating", Class: classBrowse, Path: "/library/sections/{movieSection}/all?type=1&sort=audienceRating:desc&{genreFilter}&X-Plex-Container-Start=0" + page},
		{Name: "movies filter year", Class: classBrowse, Path: all + "&year={year}"},
		{Name: "movies filter decade", Class: classBrowse, Path: all + "&decade={decade}"},
		{Name: "movies filter country", Class: classBrowse, Path: all + "&{countryFilter}"},
		{Name: "movies filter studio", Class: classBrowse, Path: all + "&studio={studio}"},
		{Name: "movies filter actor", Class: classBrowse, Path: all + "&actor={actorId}"},
		{Name: "movies filter director", Class: classBrowse, Path: all + "&director={directorId}"},
		{Name: "movies filter collection", Class: classBrowse, Path: all + "&collection={collectionId}"},
		{Name: "movies filter unwatched", Class: classBrowse, Path: all + "&unwatched=1"},
		{Name: "movies unwatched", Class: classBrowse, Path: "/library/sections/{movieSection}/unwatched?type=1&X-Plex-Container-Start=0" + page},
		{Name: "movies all titleSort 15k", Class: classBrowse, Path: "/library/sections/{movieSection}/all?type=1&sort=titleSort&X-Plex-Container-Start=15000" + page},
		{Name: "movies all addedAt 15k", Class: classBrowse, Path: "/library/sections/{movieSection}/all?type=1&sort=addedAt:desc&X-Plex-Container-Start=15000" + page},
		{Name: "movies year filter list", Class: classBrowse, Path: "/library/sections/{movieSection}/year?type=1"},
		{Name: "movies studio filter list", Class: classBrowse, Path: "/library/sections/{movieSection}/studio?type=1"},
		{Name: "movies country filter list", Class: classBrowse, Path: "/library/sections/{movieSection}/country?type=1"},
		// Franchises: the section's collection listing, one franchise and
		// its titles.
		{Name: "movies collections type=18", Class: classBrowse, Path: "/library/sections/{movieSection}/all?type=18&X-Plex-Container-Start=0&X-Plex-Container-Size=50", Discover: discoverPlexItem("collectionKey", "collection")},
		{Name: "movies collection dir", Class: classBrowse, Path: "/library/sections/{movieSection}/collection"},
		{Name: "collection", Class: classDetail, Path: "/library/collections/{collectionKey}"},
		{Name: "collection children", Class: classDetail, Path: "/library/collections/{collectionKey}/children?excludeAllLeaves=1"},
		// Person pages behind a Cast & Crew tile: the numeric form (Apple TV,
		// iOS) and the tagKey form (Plex Web).
		{Name: "person", Class: classDetail, Path: "/library/people/{actorId}"},
		{Name: "person media", Class: classDetail, Path: "/library/people/{actorId}/media"},
		{Name: "person media tagKey", Class: classDetail, Path: "/library/people/{actorTagKey}/media"},
		{Name: "director media", Class: classDetail, Path: "/library/people/{directorId}/media"},
		// A hub row's "more" page and its next page.
		{Name: "hub more", Class: classHome, Path: "{hubMore}"},
		{Name: "hub more p2", Class: classHome, Path: "{hubMoreP2}"},
		{Name: "/hubs/sections/movies/manage", Class: classHome, Path: "/hubs/sections/{movieSection}/manage"},
		{Name: "/hubs/continueWatching/items", Class: classHome, Path: "/hubs/continueWatching/items?X-Plex-Container-Start=0&X-Plex-Container-Size=50"},
		{Name: "shows onDeck", Class: classHome, Path: "/library/sections/{showSection}/onDeck"},
		{Name: "shows recentlyViewed", Class: classHome, Path: "/library/sections/{showSection}/recentlyViewedShows?X-Plex-Container-Start=0&X-Plex-Container-Size=50"},
		// Playlists: the viewer's largest, its first and a deep page.
		{Name: "playlists", Class: classBrowse, Path: "/playlists?playlistType=video", Discover: discoverPlexPlaylist},
		{Name: "playlist", Class: classDetail, Path: "/playlists/{playlistKey}"},
		{Name: "playlist items", Class: classBrowse, Path: "/playlists/{playlistKey}/items?X-Plex-Container-Start=0&X-Plex-Container-Size=50"},
		{Name: "playlist items deep", Class: classBrowse, Path: "/playlists/{playlistKey}/items?X-Plex-Container-Start=2500&X-Plex-Container-Size=50"},
	}
}

// plexPlayback is Plex for Apple TV watching: it builds play queues, reports
// the timeline while playing, then scrobbles, rates and unscrobbles, and
// reads Home's continue rows after the writes. It runs as its own client,
// after the others in a pass, so its writes never turn a cached Home the
// other clients measure into a rebuild within that pass.
func plexPlayback() Client {
	pq := "&shuffle=0&repeat=0&own=1&includeChapters=1&includeMarkers=1&includeGeolocation=1&includeExternalMedia=1"
	uri := func(key string) string {
		return "uri=" + url.QueryEscape("server://") + "{machineId}" + url.QueryEscape("/com.plexapp.plugins.library/library/metadata/") + key
	}
	timeline := func(state, at string) string {
		return "/:/timeline?ratingKey={movieKey}&key=%2Flibrary%2Fmetadata%2F{movieKey}&playQueueItemID={playQueueItemID}&state=" + state + "&hasMDE=1&time=" + at + "&duration=5400000&playbackTime=" + at
	}
	lib := "&identifier=com.plexapp.plugins.library"
	return Client{Name: "plex-appletv playback", Surface: "plex", Headers: plexDeviceHeaders("Plex for Apple TV", "tvOS", "Apple TV", "-player"), Steps: []Step{
		{Name: "/identity", Class: classStatic, Path: "/identity", Discover: discoverPlexMachine},
		{Name: "/library/sections", Class: classBrowse, Path: "/library/sections", Discover: discoverPlexSections},
		{Name: "movies all titleSort p1", Class: classBrowse, Path: "/library/sections/{movieSection}/all?type=1&sort=titleSort&X-Plex-Container-Start=0&X-Plex-Container-Size=60", Discover: discoverPlexItem("movieKey", "movie")},
		{Name: "shows all titleSort p1", Class: classBrowse, Path: "/library/sections/{showSection}/all?type=2&sort=titleSort&X-Plex-Container-Start=0&X-Plex-Container-Size=60", Discover: discoverPlexItem("showKey", "show")},
		{Name: "shows allLeaves p1", Class: classBrowse, Path: "/library/sections/{showSection}/allLeaves?X-Plex-Container-Start=0&X-Plex-Container-Size=50", Discover: discoverPlexItem("episodeKey", "episode")},
		{Name: "playlists", Class: classBrowse, Path: "/playlists?playlistType=video", Discover: discoverPlexPlaylist},
		{Name: "playQueue movie", Class: classState, Method: http.MethodPost, Path: "/playQueues?type=video&" + uri("{movieKey}") + "&continuous=0" + pq, Discover: discoverPlayQueue},
		{Name: "playQueue get", Class: classState, Path: "/playQueues/{playQueueID}?own=1&includeChapters=1&includeMarkers=1"},
		{Name: "playQueue show continuous", Class: classState, Method: http.MethodPost, Path: "/playQueues?type=video&" + uri("{showKey}") + "&continuous=1" + pq},
		{Name: "playQueue playlist shuffle", Class: classState, Method: http.MethodPost, Path: "/playQueues?type=video&playlistID={playlistKey}&shuffle=1&own=1&includeChapters=1&includeMarkers=1"},
		{Name: "timeline playing", Class: classState, Path: timeline("playing", "60000")},
		{Name: "timeline paused", Class: classState, Path: timeline("paused", "120000")},
		{Name: "progress", Class: classState, Path: "/:/progress?key={movieKey}" + lib + "&time=180000&state=playing"},
		{Name: "timeline stopped", Class: classState, Path: timeline("stopped", "180000")},
		{Name: "rate", Class: classState, Method: http.MethodPut, Path: "/:/rate?key={movieKey}" + lib + "&rating=8"},
		{Name: "scrobble movie", Class: classState, Path: "/:/scrobble?key={movieKey}" + lib},
		{Name: "/hubs/continueWatching after scrobble", Class: classHome, Path: "/hubs/continueWatching?count=12&includeMeta=1&excludeFields=summary"},
		{Name: "unscrobble movie", Class: classState, Path: "/:/unscrobble?key={movieKey}" + lib},
		{Name: "scrobble episode", Class: classState, Path: "/:/scrobble?key={episodeKey}" + lib},
		{Name: "/library/onDeck after scrobble", Class: classHome, Path: "/library/onDeck"},
		{Name: "unscrobble episode", Class: classState, Path: "/:/unscrobble?key={episodeKey}" + lib},
	}}
}

func plexMetadata0(body []byte) map[string]json.RawMessage {
	var c struct {
		MediaContainer struct {
			Metadata []map[string]json.RawMessage `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if json.Unmarshal(body, &c) != nil || len(c.MediaContainer.Metadata) == 0 {
		return nil
	}
	return c.MediaContainer.Metadata[0]
}

type plexTagJSON struct {
	ID     int64  `json:"id"`
	Filter string `json:"filter"`
	Tag    string `json:"tag"`
	TagKey string `json:"tagKey"`
}

// discoverPlexTags takes the filters a detail page offers: its first cast
// member, director, genre, country, franchise, studio and year.
func discoverPlexTags(vars map[string]string, body []byte) {
	m := plexMetadata0(body)
	if m == nil {
		return
	}
	first := func(field string) (plexTagJSON, bool) {
		var tags []plexTagJSON
		if json.Unmarshal(m[field], &tags) != nil || len(tags) == 0 {
			return plexTagJSON{}, false
		}
		return tags[0], true
	}
	if t, ok := first("Role"); ok {
		setOnce(vars, "actorId", strconv.FormatInt(t.ID, 10))
		setOnce(vars, "actorTagKey", t.TagKey)
	}
	if t, ok := first("Director"); ok {
		setOnce(vars, "directorId", strconv.FormatInt(t.ID, 10))
	}
	// Clients follow a tag's filter ("genre=Action"), as PMS sends it.
	if t, ok := first("Genre"); ok {
		setOnce(vars, "genreFilter", t.Filter)
	}
	if t, ok := first("Country"); ok {
		setOnce(vars, "countryFilter", t.Filter)
	}
	if t, ok := first("Collection"); ok {
		setOnce(vars, "collectionId", strconv.FormatInt(t.ID, 10))
	}
	var studio string
	if json.Unmarshal(m["studio"], &studio) == nil && studio != "" {
		setOnce(vars, "studio", url.QueryEscape(studio))
	}
	var year int
	if json.Unmarshal(m["year"], &year) == nil && year > 0 {
		setOnce(vars, "year", strconv.Itoa(year))
		setOnce(vars, "decade", strconv.Itoa(year/10*10))
	}
}

// discoverPlexHubMore takes the first hub that offers a "more" page.
func discoverPlexHubMore(vars map[string]string, body []byte) {
	var c struct {
		MediaContainer struct {
			Hub []struct {
				Key  string `json:"key"`
				More bool   `json:"more"`
			} `json:"Hub"`
		} `json:"MediaContainer"`
	}
	if json.Unmarshal(body, &c) != nil {
		return
	}
	for _, h := range c.MediaContainer.Hub {
		if h.More && strings.HasPrefix(h.Key, "/") {
			sep := "?"
			if strings.Contains(h.Key, "?") {
				sep = "&"
			}
			setOnce(vars, "hubMore", h.Key+sep+"X-Plex-Container-Start=0&X-Plex-Container-Size=24")
			setOnce(vars, "hubMoreP2", h.Key+sep+"X-Plex-Container-Start=24&X-Plex-Container-Size=24")
			return
		}
	}
}

// discoverPlexPlaylist takes the viewer's largest playlist.
func discoverPlexPlaylist(vars map[string]string, body []byte) {
	var c struct {
		MediaContainer struct {
			Metadata []struct {
				RatingKey string `json:"ratingKey"`
				LeafCount int    `json:"leafCount"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if json.Unmarshal(body, &c) != nil {
		return
	}
	best, most := "", -1
	for _, p := range c.MediaContainer.Metadata {
		if p.LeafCount > most {
			best, most = p.RatingKey, p.LeafCount
		}
	}
	setOnce(vars, "playlistKey", best)
}

func discoverPlexMachine(vars map[string]string, body []byte) {
	var c struct {
		MediaContainer struct {
			MachineIdentifier string `json:"machineIdentifier"`
		} `json:"MediaContainer"`
	}
	if json.Unmarshal(body, &c) == nil {
		setOnce(vars, "machineId", c.MediaContainer.MachineIdentifier)
	}
}

// discoverPlayQueue takes a new play queue's id and its selected item. A
// play queue is new on every pass, so these are overwritten, not set once.
func discoverPlayQueue(vars map[string]string, body []byte) {
	var c struct {
		MediaContainer struct {
			PlayQueueID             json.Number `json:"playQueueID"`
			PlayQueueSelectedItemID json.Number `json:"playQueueSelectedItemID"`
		} `json:"MediaContainer"`
	}
	if json.Unmarshal(body, &c) != nil {
		return
	}
	if id := c.MediaContainer.PlayQueueID.String(); id != "" {
		vars["playQueueID"] = id
	}
	if id := c.MediaContainer.PlayQueueSelectedItemID.String(); id != "" {
		vars["playQueueItemID"] = id
	}
}

var siloPersonID = regexp.MustCompile(`"person_id"\s*:\s*"?([^",}\s]+)`)

// siloLibrarySteps are Silo's paths past the first catalogue page: a deep
// cursor, search, a famous title's cast, person pages, and the watch-state
// writes the app sends from a detail page, each undone in the same pass.
func siloLibrarySteps() []Step {
	cat := "/api/v2/catalog?backdrop_width=2688&library_id={movieLibrary}&limit=100&match=all&poster_width=520&source=query&type=movie"
	return []Step{
		{Name: "catalog movies title cursor 15k", Class: classBrowse, Path: cat + "&sort=title&cursor=15000"},
		{Name: "catalog search star", Class: classSearch, RowsBound: searchRowsBound, Path: "/api/v2/catalog?backdrop_width=2688&limit=50&poster_width=520&q=star"},
		{Name: "catalog item movie credits", Class: classDetail, Path: "/api/v2/catalog/items/{movieId}?backdrop_width=2688&logo_width=1760&photo_width=400&poster_width=920", Discover: discoverRegexp("personId", siloPersonID)},
		{Name: "person", Class: classDetail, Path: "/api/v2/catalog/people/{personId}?photo_width=400&poster_width=440"},
		{Name: "watched movie", Class: classState, Method: http.MethodPost, Path: "/api/v2/watched/{movieId}"},
		{Name: "home/sections after watched", Class: classHome, Path: "/api/v2/home/sections?backdrop_width=2688&image_size=medium&logo_width=1760&item_limit=20"},
		{Name: "unwatched movie", Class: classState, Method: http.MethodDelete, Path: "/api/v2/watched/{movieId}"},
		{Name: "favorite movie", Class: classState, Method: http.MethodPut, Path: "/api/v2/favorites/{movieId}"},
		{Name: "favorite get", Class: classDetail, Path: "/api/v2/favorites/{movieId}"},
		{Name: "unfavorite movie", Class: classState, Method: http.MethodDelete, Path: "/api/v2/favorites/{movieId}"},
		{Name: "watchlist movie", Class: classState, Method: http.MethodPut, Path: "/api/v2/watchlist/{movieId}"},
		{Name: "unwatchlist movie", Class: classState, Method: http.MethodDelete, Path: "/api/v2/watchlist/{movieId}"},
	}
}
