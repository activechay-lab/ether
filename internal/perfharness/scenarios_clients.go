package perfharness

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
)

// Library-scale paths of the Jellyfin, Emby, Stremio, Xtream, console and
// MCP clients beyond each client's first screens: deep pages, facet
// filters and lists, watch-state writes, IPTV exports and MCP tools.

// jellyfinLibrarySteps are Infuse and Jellyfin web's filtered, sorted and
// deep listings, facet lists and watch-state writes, with Infuse's fields.
func jellyfinLibrarySteps(f string) []Step {
	movies := "/Items?excludeLocationTypes=Virtual&fields=" + f + "&includeItemTypes=Movie&limit=50&parentId={moviesView}&recursive=true&userId={userId}"
	return []Step{
		// Infuse's library sync pages in order: page 2 follows page 1 (the
		// remembered page end lets it seek), the jumps below do not.
		{Name: "movies p2 SortName", Class: classBrowse, Path: movies + "&sortBy=SortName&sortOrder=Ascending&startIndex=50"},
		{Name: "movies p3 SortName", Class: classBrowse, Path: movies + "&sortBy=SortName&sortOrder=Ascending&startIndex=100"},
		{Name: "movies very deep SortName", Class: classBrowse, Path: movies + "&sortBy=SortName&sortOrder=Ascending&startIndex=24000"},
		{Name: "movies deep DateCreated", Class: classBrowse, Path: movies + "&sortBy=DateCreated%2CSortName&sortOrder=Descending&startIndex=12000"},
		{Name: "movies p1 CommunityRating", Class: classBrowse, Path: movies + "&sortBy=CommunityRating%2CSortName&sortOrder=Descending&startIndex=0"},
		{Name: "movies p1 PremiereDate", Class: classBrowse, Path: movies + "&sortBy=PremiereDate%2CSortName&sortOrder=Descending&startIndex=0"},
		{Name: "movies p1 Genres=Drama", Class: classBrowse, Path: movies + "&genres=Drama&sortBy=SortName&sortOrder=Ascending&startIndex=0"},
		{Name: "movies deep Genres=Drama", Class: classBrowse, Path: movies + "&genres=Drama&sortBy=SortName&sortOrder=Ascending&startIndex=1000"},
		{Name: "movies p1 Years", Class: classBrowse, Path: movies + "&years=1999&sortBy=SortName&sortOrder=Ascending&startIndex=0"},
		{Name: "movies p1 IsPlayed", Class: classBrowse, Path: movies + "&filters=IsPlayed&sortBy=SortName&sortOrder=Ascending&startIndex=0"},
		{Name: "movies p1 IsUnplayed", Class: classBrowse, Path: movies + "&filters=IsUnplayed&sortBy=SortName&sortOrder=Ascending&startIndex=0"},
		{Name: "movies p1 IsFavorite", Class: classBrowse, Path: movies + "&filters=IsFavorite&sortBy=SortName&sortOrder=Ascending&startIndex=0"},
		{Name: "series deep SortName", Class: classBrowse, Path: "/Items?excludeLocationTypes=Virtual&fields=" + f + "&includeItemTypes=Series&limit=50&parentId={showsView}&recursive=true&sortBy=SortName&sortOrder=Ascending&startIndex=600&userId={userId}"},
		{Name: "episodes recursive MediaSources p2", Class: classBrowse, Path: "/Users/{userId}/Items?IncludeItemTypes=Episode&Recursive=true&Fields=MediaSources&Limit=400&StartIndex=400"},
		{Name: "/Genres movies", Class: classBrowse, Path: "/Genres?parentId={moviesView}&includeItemTypes=Movie&sortBy=SortName&sortOrder=Ascending&enableImages=false&userId={userId}", Discover: discoverMBItem("genreId", "Genre")},
		{Name: "movies p1 GenreIds", Class: classBrowse, Path: movies + "&genreIds={genreId}&sortBy=SortName&sortOrder=Ascending&startIndex=0"},
		{Name: "/Studios movies", Class: classBrowse, Path: "/Studios?parentId={moviesView}&includeItemTypes=Movie&sortBy=SortName&sortOrder=Ascending&enableImages=false&userId={userId}", Discover: discoverMBItem("studioId", "Studio")},
		{Name: "movies p1 StudioIds", Class: classBrowse, Path: movies + "&studioIds={studioId}&sortBy=SortName&sortOrder=Ascending&startIndex=0"},
		{Name: "/Persons", Class: classBrowse, Path: "/Persons?limit=100&personTypes=Actor&enableImages=true&userId={userId}"},
		{Name: "movie detail People", Class: classDetail, Path: "/Items/{movieId}?fields=" + f + "%2CPeople%2CStudios%2CTaglines&userId={userId}", Discover: discoverMBPerson},
		{Name: "movies p1 PersonIds", Class: classBrowse, Path: "/Items?fields=" + f + "&includeItemTypes=Movie%2CSeries&limit=50&personIds={personId}&recursive=true&sortBy=SortName&sortOrder=Ascending&startIndex=0&userId={userId}"},
		{Name: "movie Similar", Class: classDetail, Path: "/Items/{movieId}/Similar?fields=" + f + "&limit=12&userId={userId}"},
		{Name: "/Movies/Recommendations", Class: classHome, Path: "/Movies/Recommendations?categoryLimit=6&itemLimit=8&fields=" + f + "&userId={userId}"},
		{Name: "/Search/Hints star", Class: classSearch, RowsBound: searchRowsBound, Path: "/Search/Hints?searchTerm=star&limit=24&userId={userId}"},
		{Name: "progress report", Class: classDetail, Method: http.MethodPost, Path: "/Sessions/Playing/Progress", Body: `{"ItemId":"{movieId}","PositionTicks":6000000000,"IsPaused":false}`},
		{Name: "mark played", Class: classDetail, Method: http.MethodPost, Path: "/UserPlayedItems/{movieId}?userId={userId}"},
		{Name: "mark unplayed", Class: classDetail, Method: http.MethodDelete, Path: "/UserPlayedItems/{movieId}?userId={userId}"},
		{Name: "mark series played", Class: classDetail, Method: http.MethodPost, Path: "/UserPlayedItems/{seriesId}?userId={userId}"},
		{Name: "mark series unplayed", Class: classDetail, Method: http.MethodDelete, Path: "/UserPlayedItems/{seriesId}?userId={userId}"},
		{Name: "favorite", Class: classDetail, Method: http.MethodPost, Path: "/UserFavoriteItems/{movieId}?userId={userId}"},
		{Name: "unfavorite", Class: classDetail, Method: http.MethodDelete, Path: "/UserFavoriteItems/{movieId}?userId={userId}"},
	}
}

// embyLibrarySteps are the Emby apps' deep pages, filters, facet lists and
// watch-state writes.
func embyLibrarySteps() []Step {
	movies := "/Users/{userId}/Items?ParentId={moviesView}&Recursive=true&IncludeItemTypes=Movie&Limit=100&Fields=PrimaryImageAspectRatio%2CBasicSyncInfo%2CProductionYear"
	return []Step{
		{Name: "movies very deep", Class: classBrowse, Path: movies + "&SortBy=SortName&SortOrder=Ascending&StartIndex=24000"},
		{Name: "movies DateCreated deep", Class: classBrowse, Path: movies + "&SortBy=DateCreated%2CSortName&SortOrder=Descending&StartIndex=12000"},
		{Name: "/Genres", Class: classBrowse, Path: "/Genres?UserId={userId}&ParentId={moviesView}&IncludeItemTypes=Movie&SortBy=SortName&Recursive=true", Discover: discoverMBItem("genreId", "Genre")},
		{Name: "movies GenreIds", Class: classBrowse, Path: movies + "&GenreIds={genreId}&SortBy=SortName&SortOrder=Ascending&StartIndex=0"},
		{Name: "/Studios", Class: classBrowse, Path: "/Studios?UserId={userId}&ParentId={moviesView}&IncludeItemTypes=Movie&SortBy=SortName&Recursive=true"},
		{Name: "/Persons", Class: classBrowse, Path: "/Persons?UserId={userId}&Recursive=true&PersonTypes=Actor&Limit=100"},
		{Name: "movies Filters=IsPlayed", Class: classBrowse, Path: movies + "&Filters=IsPlayed&SortBy=SortName&SortOrder=Ascending&StartIndex=0"},
		{Name: "favorites", Class: classHome, Path: "/Users/{userId}/Items?Recursive=true&Filters=IsFavorite&IncludeItemTypes=Movie%2CSeries&Limit=24&SortBy=SortName"},
		{Name: "progress report", Class: classDetail, Method: http.MethodPost, Path: "/Sessions/Playing/Progress", Body: `{"ItemId":"{movieId}","PositionTicks":6000000000,"IsPaused":false}`},
		{Name: "mark played", Class: classDetail, Method: http.MethodPost, Path: "/Users/{userId}/PlayedItems/{movieId}"},
		{Name: "mark unplayed", Class: classDetail, Method: http.MethodDelete, Path: "/Users/{userId}/PlayedItems/{movieId}"},
		{Name: "favorite", Class: classDetail, Method: http.MethodPost, Path: "/Users/{userId}/FavoriteItems/{movieId}"},
		{Name: "unfavorite", Class: classDetail, Method: http.MethodDelete, Path: "/Users/{userId}/FavoriteItems/{movieId}"},
	}
}

// stremioLibrarySteps are Stremio's deep catalog pages: the app pages by
// skip as the viewer scrolls a board or a discover column.
func stremioLibrarySteps() []Step {
	return []Step{
		{Name: "catalog movies skip=10000", Class: classBrowse, Path: "/catalog/movie/ether-movies/skip=10000.json"},
		{Name: "catalog movies genre skip=1000", Class: classBrowse, Path: "/catalog/movie/ether-movies/genre=Action&skip=1000.json"},
		{Name: "catalog series skip=600", Class: classBrowse, Path: "/catalog/series/ether-series/skip=600.json"},
		{Name: "catalog series genre", Class: classBrowse, Path: "/catalog/series/ether-series/genre=Drama.json"},
		{Name: "catalog series search", Class: classSearch, RowsBound: searchRowsBound, Path: "/catalog/series/ether-series/search=star.json"},
	}
}

// consoleLibrarySteps are the owner console's catalogue browser, stats,
// viewer and title pages beyond its first screens.
func consoleLibrarySteps() []Step {
	return []Step{
		{Name: "catalog browse genre", Class: classConsole, Path: "/catalog/browse?type=movie&genre=Drama&offset=0&limit=48"},
		{Name: "catalog browse shows", Class: classConsole, Path: "/catalog/browse?type=show&offset=0&limit=48", Discover: discoverConsoleKey},
		{Name: "catalog title", Class: classConsole, Path: "/catalog/title/{consoleKey}"},
		{Name: "catalog title season", Class: classConsole, Path: "/catalog/title/{consoleKey}/season/1"},
		{Name: "users", Class: classConsole, Path: "/users"},
		{Name: "viewer", Class: classConsole, Path: "/viewers/{viewerId}"},
		{Name: "viewer taste", Class: classConsole, Path: "/viewers/{viewerId}/taste"},
		{Name: "stats history", Class: classConsole, Path: "/stats/history"},
		{Name: "stats summary", Class: classConsole, Path: "/stats/summary"},
		{Name: "experience", Class: classConsole, Path: "/experience/"},
		{Name: "experience viewer", Class: classConsole, Path: "/experience/viewers/{viewerId}"},
		{Name: "insights ranker", Class: classConsole, Path: "/insights/ranker"},
	}
}

var consoleKey = regexp.MustCompile(`"key"\s*:\s*"?([0-9]+)`)

func discoverConsoleKey(vars map[string]string, body []byte) {
	if m := consoleKey.FindSubmatch(body); m != nil {
		setOnce(vars, "consoleKey", string(m[1]))
	}
}

func discoverConsoleCursor(vars map[string]string, body []byte) {
	var r struct {
		Next string `json:"nextCursor"`
	}
	if json.Unmarshal(body, &r) == nil {
		setOnce(vars, "consoleCursor", url.QueryEscape(r.Next))
	}
}

// discoverMBPerson records the first credited person of an item detail.
func discoverMBPerson(vars map[string]string, body []byte) {
	var r struct {
		People []struct {
			ID string `json:"Id"`
		} `json:"People"`
	}
	if json.Unmarshal(body, &r) == nil {
		for _, p := range r.People {
			setOnce(vars, "personId", p.ID)
		}
	}
}

var (
	xtreamStreamID = regexp.MustCompile(`"stream_id":([0-9]+)`)
	xtreamSeriesID = regexp.MustCompile(`"series_id":([0-9]+)`)
)

// xtream is an IPTV player app (TiviMate, IPTV Smarters) against Ether's
// Xtream API: it signs in, lists categories, downloads the whole VOD and
// series lists (the apps cache them) and opens a title's info.
func xtream() Client {
	api := "/player_api.php?username={xtreamUser}&password={xtreamPass}"
	return Client{Name: "xtream", Surface: "xtream", Prefix: "/xtream", Headers: func(*session) http.Header {
		h := http.Header{}
		h.Set("Accept", "application/json")
		h.Set("User-Agent", "IPTVSmartersPro")
		return h
	}, Steps: []Step{
		{Name: "player_api", Class: classStatic, Path: api},
		{Name: "get_vod_categories", Class: classBrowse, Path: api + "&action=get_vod_categories"},
		{Name: "get_series_categories", Class: classBrowse, Path: api + "&action=get_series_categories"},
		{Name: "get_live_categories", Class: classGuide, Path: api + "&action=get_live_categories"},
		{Name: "get_live_streams", Class: classGuide, Path: api + "&action=get_live_streams"},
		{Name: "get_vod_streams", Class: classBrowse, Export: true, Path: api + "&action=get_vod_streams", Discover: discoverRegexp("xtreamVod", xtreamStreamID)},
		{Name: "get_series", Class: classBrowse, Export: true, Path: api + "&action=get_series", Discover: discoverRegexp("xtreamSeries", xtreamSeriesID)},
		{Name: "get_vod_info", Class: classDetail, Path: api + "&action=get_vod_info&vod_id={xtreamVod}"},
		{Name: "get_series_info", Class: classDetail, Path: api + "&action=get_series_info&series_id={xtreamSeries}"},
	}}
}

var mcpTitleKey = regexp.MustCompile(`\\?"key\\?"\s*:\s*\\?"([0-9]+)`)

// mcpClient is an AI app paired over MCP with a read grant: it opens the
// session, lists tools and calls the catalogue, history and collection tools.
func mcpClient() Client {
	call := func(id int, tool, args string) string {
		return `{"jsonrpc":"2.0","id":` + strconv.Itoa(id) + `,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + args + `}}`
	}
	return Client{Name: "mcp", Surface: "mcp", Prefix: "/ether/mcp", Headers: func(s *session) http.Header {
		h := http.Header{}
		h.Set("Accept", "application/json, text/event-stream")
		h.Set("Content-Type", "application/json")
		h.Set("MCP-Protocol-Version", "2025-06-18")
		h.Set("Authorization", "Bearer "+s.vars["mcp"])
		return h
	}, Steps: []Step{
		{Name: "initialize", Class: classStatic, Method: http.MethodPost, Body: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"etherperf","version":"1"}}}`},
		{Name: "tools/list", Class: classStatic, Method: http.MethodPost, Body: `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`},
		{Name: "catalog_search", Class: classSearch, RowsBound: searchRowsBound, Method: http.MethodPost, Body: call(3, "catalog_search", `{"query":"star","limit":25}`), Discover: discoverRegexp("mcpKey", mcpTitleKey)},
		{Name: "title_details", Class: classDetail, Method: http.MethodPost, Body: call(4, "title_details", `{"titleKey":"{mcpKey}"}`)},
		{Name: "playback_history", Class: classConsole, Method: http.MethodPost, Body: call(5, "playback_history", `{"pageSize":50}`)},
		{Name: "playback_history deep", Class: classConsole, Method: http.MethodPost, Body: call(6, "playback_history", `{"page":10,"pageSize":50}`)},
		{Name: "collection_list", Class: classConsole, Method: http.MethodPost, Body: call(7, "collection_list", `{}`)},
		{Name: "server_status", Class: classConsole, Method: http.MethodPost, Body: call(8, "server_status", `{}`)},
	}}
}
