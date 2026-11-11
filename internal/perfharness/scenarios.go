package perfharness

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

// Budget classes. Every step belongs to one; budgets.go holds the numbers.
const (
	classStatic   = "static"   // identity, system info, capabilities
	classSignIn   = "signin"   // password sign-in through a protocol
	classHome     = "home"     // home screens, hubs, continue watching
	classBrowse   = "browse"   // library listings, section pages, catalogs
	classDetail   = "detail"   // one item, its seasons and episodes
	classSearch   = "search"   // search as you type
	classGuide    = "guide"    // live TV guide grid
	classConsole  = "console"  // owner console pages
	classPlayback = "playback" // playback setup (reported, not gated: needs providers)
)

// step is one request a client makes. Path and Body are templates: {name}
// is replaced from the session's variables; a step whose variables were not
// discovered is reported as skipped rather than guessed.
// searchRowsBound bounds a search step's rows: these searches read at most
// 100 to 200 candidates (experience's searchWindowMin for a first page,
// Plex's hub limits), each hydrated with its genres, keywords and credits,
// however many titles match.
const searchRowsBound = 1500

type Step struct {
	Name  string
	Class string
	// RowsBound, when set, is a hard bound on the rows the step reads,
	// whatever the library's size: a search reads at most its candidate
	// window, so its rows grow with the matches only until the window
	// fills. The gate checks the bound instead of comparing scales.
	RowsBound int
	// QueriesBound, when set, is a hard bound on the statements one
	// request of the step runs, whatever the library's size, and the step
	// must write nothing: a read that loops over its titles or rows, or
	// saves what it shows, breaks it before the scale comparison would.
	QueriesBound int
	// Export marks a step whose response is the whole library by design
	// (an IPTV app's VOD list): its work grows with what it renders, so
	// the gate compares work per response byte instead of per request.
	Export   bool
	Method   string
	Path     string
	Body     string
	Discover func(vars map[string]string, body []byte)
}

// Statement ceilings for Plex Home, detail, post-play and search. Measured
// on the production snapshot (58k titles, 30 viewers) at 200-350 statements
// for a Home request, 12-65 for detail and post-play and 43 for a search,
// down from 600, 70, 62 and 43 with 30 writes on Home; the synthetic gate
// library has a heavier viewer history (20k watched, 40 in progress).
const (
	homeQueriesBound   = 500
	detailQueriesBound = 120
	searchQueriesBound = 100
	// cachedHomeQueriesBound: a viewer's Home already built for a client of
	// the same kind is served from memory after one revision read
	// (plex/home_cache.go), however large the library or the viewer's model.
	cachedHomeQueriesBound = 5
)

// client is one real client's request sequence against one protocol.
type Client struct {
	Name    string
	Surface string
	// Port offset from the base port (0 main, 1 Jellyfin, 2 Emby, 3 Silo),
	// or SharedPrefix on the main port when the runner shares one port.
	Port         int
	SharedPrefix string
	Prefix       string
	Headers      func(s *session) http.Header
	SignIn       *Step
	Steps        []Step
}

// session is one viewer driving one client.
type session struct {
	Viewer Viewer
	vars   map[string]string
}

var templateVar = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)

// expand fills a template; ok is false when a variable is missing.
func expand(tpl string, vars map[string]string) (string, string, bool) {
	missing := ""
	out := templateVar.ReplaceAllStringFunc(tpl, func(m string) string {
		name := m[1 : len(m)-1]
		v, ok := vars[name]
		if !ok || v == "" {
			if missing == "" {
				missing = name
			}
			return m
		}
		return v
	})
	return out, missing, missing == ""
}

// Infuse's exact field list (captured from Infuse 8 against Jellyfin).
const infuseFields = "DateCreated%2CEtag%2CGenres%2CMediaSources%2CAlternateMediaSources%2COverview%2CParentId%2CPath%2CProviderIds%2CSortName%2CRecursiveItemCount%2CChildCount"

// Clients is every replayed client, in report order.
func Clients() []Client {
	return []Client{plexClient("plex-appletv", "Plex for Apple TV", "tvOS", "Apple TV"), plexSecondDevice(), plexClient("plex-android", "Plex for Android (TV)", "Android", "BRAVIA 4K VH21"), plexClient("plex-web", "Plex Web", "Chrome", "Windows"), plexPlayback(),
		infuseJellyfin(), swiftfin(), embySDK(), infuseEmby(), siloAppleTV(), stremio(), console(), xtream(), mcpClient()}
}

func plexHeaders(product, platform, device string) func(*session) http.Header {
	return plexDeviceHeaders(product, platform, device, "")
}

// plexDeviceHeaders are plexHeaders for one more device of the same kind:
// suffix makes its client identifier distinct.
func plexDeviceHeaders(product, platform, device, suffix string) func(*session) http.Header {
	return func(s *session) http.Header {
		h := http.Header{}
		h.Set("Accept", "application/json")
		h.Set("X-Plex-Product", product)
		h.Set("X-Plex-Version", "8.40")
		h.Set("X-Plex-Platform", platform)
		h.Set("X-Plex-Platform-Version", "18.0")
		h.Set("X-Plex-Device", device)
		h.Set("X-Plex-Device-Name", "etherperf")
		h.Set("X-Plex-Client-Identifier", "etherperf-"+strings.ReplaceAll(strings.ToLower(product), " ", "-")+"-"+strconv.FormatInt(s.Viewer.ID, 10)+suffix)
		h.Set("X-Plex-Provides", "player,controller")
		// A server that trusts the replaying network (an unclaimed PMS
		// with allowedNetworks) is driven without a token.
		if s.Viewer.PlexToken != "" {
			h.Set("X-Plex-Token", s.Viewer.PlexToken)
		}
		return h
	}
}

// plexSecondDevice is a viewer's second Apple TV, first seen right after
// the first one opened Home: it must be served the viewer's cached Home, not
// a fresh build (a new client identifier is not a new Home).
func plexSecondDevice() Client {
	return Client{Name: "plex-appletv second device", Surface: "plex", Headers: plexDeviceHeaders("Plex for Apple TV", "tvOS", "Apple TV", "-second"), Steps: []Step{
		{Name: "/library/sections", Class: classBrowse, Path: "/library/sections", Discover: discoverPlexSections},
		{Name: "/hubs/promoted", Class: classHome, QueriesBound: cachedHomeQueriesBound, Path: "/hubs/promoted?contentDirectoryID={movieSection},{showSection}&pinnedContentDirectoryID={movieSection},{showSection}&includeMeta=1&excludeFields=summary&count=12&includeExternalMetadata=1&includeRecentChannels=1"},
		{Name: "/hubs", Class: classHome, QueriesBound: cachedHomeQueriesBound, Path: "/hubs?count=12&includeMeta=1&excludeFields=summary"},
		{Name: "/hubs/promoted bare", Class: classHome, QueriesBound: cachedHomeQueriesBound, Path: "/hubs/promoted?count=12"},
	}}
}

func plexClient(name, product, platform, device string) Client {
	page := "&includeCollections=1&includeAdvanced=1&includeMeta=1&excludeFields=summary&X-Plex-Container-Size=60"
	return Client{Name: name, Surface: "plex", Headers: plexHeaders(product, platform, device), Steps: append([]Step{
		{Name: "/identity", Class: classStatic, Path: "/identity"},
		{Name: "/", Class: classStatic, Path: "/"},
		{Name: "/library/sections", Class: classBrowse, Path: "/library/sections", Discover: discoverPlexSections},
		{Name: "/hubs/promoted", Class: classHome, QueriesBound: homeQueriesBound, Path: "/hubs/promoted?contentDirectoryID={movieSection},{showSection}&pinnedContentDirectoryID={movieSection},{showSection}&includeMeta=1&excludeFields=summary&count=12&includeExternalMetadata=1&includeRecentChannels=1"},
		// Apple TV and Android TV ask for Home one library at a time.
		{Name: "/hubs/promoted movies directory", Class: classHome, QueriesBound: homeQueriesBound, Path: "/hubs/promoted?contentDirectoryID={movieSection}&pinnedContentDirectoryID={movieSection},{showSection}&includeMeta=1&excludeFields=summary&count=12&includeExternalMetadata=1&includeRecentChannels=1"},
		{Name: "/hubs/promoted shows directory", Class: classHome, QueriesBound: homeQueriesBound, Path: "/hubs/promoted?contentDirectoryID={showSection}&pinnedContentDirectoryID={movieSection},{showSection}&includeMeta=1&excludeFields=summary&count=12&includeExternalMetadata=1&includeRecentChannels=1"},
		{Name: "/hubs/continueWatching", Class: classHome, Path: "/hubs/continueWatching?count=12&includeMeta=1&excludeFields=summary"},
		{Name: "/hubs", Class: classHome, QueriesBound: homeQueriesBound, Path: "/hubs?count=12&includeMeta=1&excludeFields=summary"},
		// The shape production logged for Apple TV and Plex Web's Home.
		{Name: "/hubs/promoted bare", Class: classHome, QueriesBound: homeQueriesBound, Path: "/hubs/promoted?count=12"},
		{Name: "/library/onDeck", Class: classHome, Path: "/library/onDeck"},
		{Name: "/hubs/sections/movies", Class: classHome, Path: "/hubs/sections/{movieSection}?count=12&includeMeta=1&excludeFields=summary&includeLibraryPlaylists=1", Discover: discoverPlexHubMore},
		{Name: "/hubs/sections/shows", Class: classHome, Path: "/hubs/sections/{showSection}?count=12&includeMeta=1&excludeFields=summary"},
		{Name: "movies all titleSort p1", Class: classBrowse, Path: "/library/sections/{movieSection}/all?type=1&sort=titleSort&X-Plex-Container-Start=0" + page, Discover: discoverPlexItem("movieKey", "movie")},
		{Name: "movies all titleSort deep", Class: classBrowse, Path: "/library/sections/{movieSection}/all?type=1&sort=titleSort&X-Plex-Container-Start=6000" + page},
		{Name: "movies all addedAt", Class: classBrowse, Path: "/library/sections/{movieSection}/all?type=1&sort=addedAt:desc&X-Plex-Container-Start=0" + page},
		{Name: "movies all rating", Class: classBrowse, Path: "/library/sections/{movieSection}/all?type=1&sort=rating:desc&X-Plex-Container-Start=0" + page},
		{Name: "movies genre filter list", Class: classBrowse, Path: "/library/sections/{movieSection}/genre?type=1"},
		{Name: "movies jump bar", Class: classBrowse, Path: "/library/sections/{movieSection}/firstCharacter?type=1"},
		{Name: "shows all titleSort p1", Class: classBrowse, Path: "/library/sections/{showSection}/all?type=2&sort=titleSort&X-Plex-Container-Start=0" + page, Discover: discoverPlexItem("showKey", "show")},
		{Name: "shows allLeaves p1", Class: classBrowse, Path: "/library/sections/{showSection}/allLeaves?includeGuids=1&X-Plex-Container-Start=0&X-Plex-Container-Size=100", Discover: discoverPlexItem("episodeKey", "episode")},
		{Name: "shows allLeaves deep", Class: classBrowse, Path: "/library/sections/{showSection}/allLeaves?includeGuids=1&X-Plex-Container-Start=6000&X-Plex-Container-Size=100"},
		{Name: "shows all type=4 p1", Class: classBrowse, Path: "/library/sections/{showSection}/all?type=4&includeGuids=1&X-Plex-Container-Start=0&X-Plex-Container-Size=100"},
		{Name: "shows all type=3 p1", Class: classBrowse, Path: "/library/sections/{showSection}/all?type=3&includeGuids=1&X-Plex-Container-Start=0&X-Plex-Container-Size=100"},
		{Name: "shows allLeaves updatedAt", Class: classBrowse, Path: "/library/sections/{showSection}/allLeaves?includeGuids=1&sort=updatedAt:desc&X-Plex-Container-Start=0&X-Plex-Container-Size=100"},
		{Name: "shows all type=3 updatedAt", Class: classBrowse, Path: "/library/sections/{showSection}/all?type=3&includeGuids=1&sort=updatedAt:desc&X-Plex-Container-Start=0&X-Plex-Container-Size=100"},
		{Name: "movie detail", Class: classDetail, QueriesBound: detailQueriesBound, Path: "/library/metadata/{movieKey}?includeExtras=1&includeRelated=1&includeOnDeck=1&includeChapters=1&includeGuids=1&includeMarkers=1&includeConcerts=1&includePreferences=1&includeReviews=1&asyncAugmentMetadata=1"},
		{Name: "show detail", Class: classDetail, QueriesBound: detailQueriesBound, Path: "/library/metadata/{showKey}?includeExtras=1&includeRelated=1&includeOnDeck=1&includeChapters=1&includeGuids=1&asyncAugmentMetadata=1"},
		{Name: "show children", Class: classDetail, Path: "/library/metadata/{showKey}/children?excludeAllLeaves=1", Discover: discoverPlexItem("seasonKey", "season")},
		{Name: "season children", Class: classDetail, Path: "/library/metadata/{seasonKey}/children?excludeAllLeaves=1", Discover: discoverPlexItem("episodeKey", "episode")},
		{Name: "episode detail", Class: classDetail, QueriesBound: detailQueriesBound, Path: "/library/metadata/{episodeKey}?includeMarkers=1&includeChapters=1&includeExtras=1"},
		{Name: "movie postplay", Class: classDetail, QueriesBound: detailQueriesBound, Path: "/hubs/metadata/{movieKey}/postplay"},
		{Name: "episode postplay", Class: classDetail, QueriesBound: detailQueriesBound, Path: "/hubs/metadata/{episodeKey}/postplay"},
		{Name: "/hubs/search star", Class: classSearch, RowsBound: searchRowsBound, Path: "/hubs/search?query=star&limit=30&includeCollections=1&includeExternalMedia=1"},
		{Name: "/library/search star", Class: classSearch, QueriesBound: searchQueriesBound, Path: "/library/search?query=star&limit=30&searchTypes=movies%2Ctv&includeMetadata=1"},
		{Name: "/hubs/search the", Class: classSearch, Path: "/hubs/search?query=the&limit=30&includeCollections=1"},
		{Name: "/playlists", Class: classBrowse, Path: "/playlists?playlistType=video&includeCollections=1"},
		{Name: "/media/providers", Class: classStatic, Path: "/media/providers"},
		{Name: "/livetv/dvrs", Class: classGuide, Path: "/livetv/dvrs"},
		{Name: "live guide grid 3h", Class: classGuide, Path: "/tv.plex.providers.epg.xmltv:1/grid?type=1&beginsAt%3C={now3h}&endsAt%3E={now}"},
		{Name: "live hubs", Class: classGuide, Path: "/tv.plex.providers.epg.xmltv:1/hubs?count=12"},
	}, plexLibrarySteps(page)...)}
}

func discoverPlexSections(vars map[string]string, body []byte) {
	var c struct {
		MediaContainer struct {
			Directory []struct {
				Key  string `json:"key"`
				Type string `json:"type"`
			} `json:"Directory"`
		} `json:"MediaContainer"`
	}
	if json.Unmarshal(body, &c) != nil {
		return
	}
	for _, d := range c.MediaContainer.Directory {
		switch d.Type {
		case "movie":
			setOnce(vars, "movieSection", d.Key)
		case "show":
			setOnce(vars, "showSection", d.Key)
		}
	}
}

func discoverPlexItem(name, typ string) func(map[string]string, []byte) {
	return func(vars map[string]string, body []byte) {
		var c struct {
			MediaContainer struct {
				Metadata []struct {
					RatingKey string `json:"ratingKey"`
					Type      string `json:"type"`
				} `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		if json.Unmarshal(body, &c) != nil {
			return
		}
		for _, m := range c.MediaContainer.Metadata {
			if m.Type == typ && m.RatingKey != "" {
				setOnce(vars, name, m.RatingKey)
				return
			}
		}
	}
}

func setOnce(vars map[string]string, k, v string) {
	if vars[k] == "" && v != "" {
		vars[k] = v
	}
}

func mediaBrowserHeaders(app, version, device string, emby bool) func(*session) http.Header {
	return func(s *session) http.Header {
		h := http.Header{}
		h.Set("Accept", "application/json")
		h.Set("Content-Type", "application/json")
		deviceID := "etherperf-" + strings.ToLower(strings.ReplaceAll(app, " ", "-")) + "-" + strconv.FormatInt(s.Viewer.ID, 10)
		auth := `MediaBrowser Client="` + app + `", Device="` + device + `", DeviceId="` + deviceID + `", Version="` + version + `"`
		if tok := s.vars["token"]; tok != "" {
			auth += `, Token="` + tok + `"`
			if emby {
				h.Set("X-Emby-Token", tok)
			}
		}
		if emby {
			h.Set("X-Emby-Authorization", auth)
		} else {
			h.Set("Authorization", auth)
		}
		h.Set("User-Agent", app+"/"+version)
		return h
	}
}

func mediaBrowserSignIn() *Step {
	return &Step{Name: "AuthenticateByName", Class: classSignIn, Method: http.MethodPost, Path: "/Users/AuthenticateByName",
		Body: `{"Username":"{username}","Pw":"{password}"}`, Discover: func(vars map[string]string, body []byte) {
			var r struct {
				AccessToken string `json:"AccessToken"`
				User        struct {
					ID string `json:"Id"`
				} `json:"User"`
			}
			if json.Unmarshal(body, &r) == nil {
				vars["token"], vars["userId"] = r.AccessToken, r.User.ID
			}
		}}
}

// discoverViews records the movie, show and first personal library ids.
func discoverViews(vars map[string]string, body []byte) {
	var r struct {
		Items []struct {
			ID             string `json:"Id"`
			CollectionType string `json:"CollectionType"`
		} `json:"Items"`
	}
	if json.Unmarshal(body, &r) != nil {
		return
	}
	for _, it := range r.Items {
		// Personal libraries can carry a movies or tvshows collection type
		// too; the library views are the ones without the personal bit.
		n, err := ids.ParseGUID(ids.View, it.ID)
		if err == nil && ids.PersonalView(n) {
			setOnce(vars, "personalView", it.ID)
			continue
		}
		// Libraries beyond the builtins 1 and 2 are plugin (or filter)
		// libraries; record the first so a browse step covers it when one
		// is installed.
		if err == nil && n > 2 {
			setOnce(vars, "pluginView", it.ID)
		}
		switch it.CollectionType {
		case "movies":
			setOnce(vars, "moviesView", it.ID)
		case "tvshows":
			setOnce(vars, "showsView", it.ID)
		default:
			setOnce(vars, "personalView", it.ID)
		}
	}
}

func discoverMBItem(name, typ string) func(map[string]string, []byte) {
	return func(vars map[string]string, body []byte) {
		var r struct {
			Items []struct {
				ID   string `json:"Id"`
				Type string `json:"Type"`
			} `json:"Items"`
		}
		if json.Unmarshal(body, &r) != nil {
			var list []struct {
				ID   string `json:"Id"`
				Type string `json:"Type"`
			}
			if json.Unmarshal(body, &list) != nil {
				return
			}
			r.Items = list
		}
		for _, it := range r.Items {
			if typ == "" || it.Type == typ {
				setOnce(vars, name, it.ID)
				return
			}
		}
	}
}

func infuseJellyfin() Client {
	f := infuseFields
	return Client{Name: "infuse-jellyfin", Surface: "jellyfin", Port: 1, SharedPrefix: "/jellyfin", Headers: mediaBrowserHeaders("Infuse-Library", "8.1", "Apple TV", false), SignIn: mediaBrowserSignIn(), Steps: append([]Step{
		{Name: "/System/Info/Public", Class: classStatic, Path: "/System/Info/Public"},
		{Name: "/Users/{id}", Class: classStatic, Path: "/Users/{userId}?userId={userId}"},
		{Name: "/UserItems/Resume", Class: classHome, Path: "/UserItems/Resume?fields=" + f + "&limit=40&mediaTypes=Video&userId={userId}"},
		{Name: "/Shows/NextUp", Class: classHome, Path: "/Shows/NextUp?fields=" + f + "&limit=20&startIndex=0&userId={userId}"},
		{Name: "/UserViews", Class: classHome, Path: "/UserViews?includeExternalContent=false&userId={userId}", Discover: discoverViews},
		{Name: "/Items/Latest movies", Class: classHome, Path: "/Items/Latest?fields=" + f + "&limit=20&parentId={moviesView}&userId={userId}"},
		{Name: "/Items/Latest shows", Class: classHome, Path: "/Items/Latest?fields=" + f + "&limit=20&parentId={showsView}&userId={userId}"},
		{Name: "/UserViews/GroupingOptions", Class: classStatic, Path: "/UserViews/GroupingOptions?userId={userId}"},
		{Name: "/Library/VirtualFolders", Class: classStatic, Path: "/Library/VirtualFolders?userId={userId}"},
		{Name: "/DisplayPreferences/usersettings", Class: classStatic, Path: "/DisplayPreferences/usersettings?client=emby&userId={userId}"},
		{Name: "movies p1 SortName", Class: classBrowse, Path: "/Items?excludeLocationTypes=Virtual&fields=" + f + "&includeItemTypes=Movie&limit=50&parentId={moviesView}&recursive=true&sortBy=SortName&sortOrder=Ascending&startIndex=0&userId={userId}", Discover: discoverMBItem("movieId", "Movie")},
		{Name: "movies deep SortName", Class: classBrowse, Path: "/Items?excludeLocationTypes=Virtual&fields=" + f + "&includeItemTypes=Movie&limit=50&parentId={moviesView}&recursive=true&sortBy=SortName&sortOrder=Ascending&startIndex=6000&userId={userId}"},
		{Name: "movies p1 DateCreated", Class: classBrowse, Path: "/Items?excludeLocationTypes=Virtual&fields=" + f + "&includeItemTypes=Movie&limit=50&parentId={moviesView}&recursive=true&sortBy=DateCreated%2CSortName&sortOrder=Descending&startIndex=0&userId={userId}"},
		{Name: "series p1 SortName", Class: classBrowse, Path: "/Items?excludeLocationTypes=Virtual&fields=" + f + "&includeItemTypes=Series&limit=50&parentId={showsView}&recursive=true&sortBy=SortName&sortOrder=Ascending&startIndex=0&userId={userId}", Discover: discoverMBItem("seriesId", "Series")},
		{Name: "episodes recursive MediaSources", Class: classBrowse, Path: "/Users/{userId}/Items?IncludeItemTypes=Episode&Recursive=true&Fields=MediaSources&Limit=400"},
		{Name: "personal library p1", Class: classBrowse, Path: "/Items?excludeLocationTypes=Virtual&fields=" + f + "&limit=50&parentId={personalView}&recursive=true&sortBy=SortName&sortOrder=Ascending&startIndex=0&userId={userId}"},
		{Name: "movie detail", Class: classDetail, Path: "/Items/{movieId}?fields=" + f + "&userId={userId}"},
		{Name: "movie LocalTrailers", Class: classDetail, Path: "/Items/{movieId}/LocalTrailers?fields=DateCreated%2CEtag%2CMediaSources%2CAlternateMediaSources%2CParentId%2CPath%2CSortName&userId={userId}"},
		{Name: "movie SpecialFeatures", Class: classDetail, Path: "/Items/{movieId}/SpecialFeatures?fields=DateCreated%2CEtag%2CMediaSources%2CAlternateMediaSources%2CParentId%2CPath%2CSortName&userId={userId}"},
		{Name: "movie MediaSegments", Class: classDetail, Path: "/MediaSegments/{movieId}?userId={userId}"},
		{Name: "series Seasons", Class: classDetail, Path: "/Shows/{seriesId}/Seasons?excludeLocationTypes=Virtual&fields=Genres%2CParentId&limit=200&startIndex=0&userId={userId}", Discover: discoverMBItem("seasonId", "Season")},
		{Name: "season Episodes", Class: classDetail, Path: "/Shows/{seriesId}/Episodes?excludeLocationTypes=Virtual&fields=Etag%2CMediaSources%2CAlternateMediaSources%2CGenres%2COverview%2CParentId%2CProviderIds&seasonId={seasonId}&userId={userId}", Discover: discoverMBItem("episodeId", "Episode")},
		{Name: "episode detail", Class: classDetail, Path: "/Items/{episodeId}?fields=" + f + "&userId={userId}"},
		{Name: "search items star", Class: classSearch, RowsBound: searchRowsBound, Path: "/Items?fields=" + f + "&includeItemTypes=Movie%2CSeries&limit=24&recursive=true&searchTerm=star&userId={userId}"},
		{Name: "search persons", Class: classSearch, Path: "/Persons?imageTypeLimit=1&limit=24&recursive=true&searchTerm=star&userId={userId}"},
		{Name: "movie PlaybackInfo", Class: classPlayback, Method: http.MethodPost, Path: "/Items/{movieId}/PlaybackInfo?userId={userId}", Body: `{}`},
	}, jellyfinLibrarySteps(f)...)}
}

func swiftfin() Client {
	return Client{Name: "swiftfin", Surface: "jellyfin", Port: 1, SharedPrefix: "/jellyfin", Headers: mediaBrowserHeaders("Swiftfin tvOS", "1.3", "Apple TV", false), SignIn: mediaBrowserSignIn(), Steps: []Step{
		{Name: "/Users/{id}/Views", Class: classHome, Path: "/Users/{userId}/Views", Discover: discoverViews},
		{Name: "/Users/{id}/Items/Resume", Class: classHome, Path: "/Users/{userId}/Items/Resume?enableUserData=true&fields=PrimaryImageAspectRatio%2COverview&limit=20&mediaTypes=Video"},
		{Name: "/Shows/NextUp", Class: classHome, Path: "/Shows/NextUp?enableUserData=true&fields=PrimaryImageAspectRatio%2COverview&limit=20&userId={userId}"},
		{Name: "/Users/{id}/Items/Latest", Class: classHome, Path: "/Users/{userId}/Items/Latest?enableUserData=true&fields=PrimaryImageAspectRatio%2COverview&limit=20&parentId={moviesView}"},
		{Name: "recently added (cross-library)", Class: classHome, Path: "/Items?userId={userId}&recursive=true&sortBy=DateCreated&sortOrder=Descending&includeItemTypes=Movie&includeItemTypes=Series&fields=MediaStreams,Genres,Studios&limit=20&enableUserData=true"},
		{Name: "movies p1", Class: classBrowse, Path: "/Users/{userId}/Items?enableUserData=true&fields=PrimaryImageAspectRatio%2COverview&includeItemTypes=Movie&limit=100&parentId={moviesView}&recursive=true&sortBy=SortName&sortOrder=Ascending&startIndex=0", Discover: discoverMBItem("movieId", "Movie")},
		{Name: "movie detail", Class: classDetail, Path: "/Users/{userId}/Items/{movieId}"},
	}}
}

func embySDK() Client {
	return Client{Name: "emby-sdk", Surface: "emby", Port: 2, SharedPrefix: "/emby", Headers: mediaBrowserHeaders("Emby Theater", "4.8", "etherperf", true), SignIn: mediaBrowserSignIn(), Steps: append([]Step{
		{Name: "/System/Info/Public", Class: classStatic, Path: "/System/Info/Public"},
		{Name: "/Users/{id}/Views", Class: classHome, Path: "/Users/{userId}/Views", Discover: discoverViews},
		{Name: "/Users/{id}/Items/Resume", Class: classHome, Path: "/Users/{userId}/Items/Resume?Limit=12&MediaTypes=Video&Fields=PrimaryImageAspectRatio"},
		{Name: "/Shows/NextUp", Class: classHome, Path: "/Shows/NextUp?UserId={userId}&Limit=12&Fields=PrimaryImageAspectRatio"},
		{Name: "/Users/{id}/Items/Latest", Class: classHome, Path: "/Users/{userId}/Items/Latest?ParentId={moviesView}&Limit=16&Fields=PrimaryImageAspectRatio"},
		{Name: "movies p1", Class: classBrowse, Path: "/Users/{userId}/Items?ParentId={moviesView}&Recursive=true&IncludeItemTypes=Movie&SortBy=SortName&SortOrder=Ascending&StartIndex=0&Limit=100&Fields=PrimaryImageAspectRatio%2CBasicSyncInfo%2CProductionYear", Discover: discoverMBItem("movieId", "Movie")},
		{Name: "movies deep", Class: classBrowse, Path: "/Users/{userId}/Items?ParentId={moviesView}&Recursive=true&IncludeItemTypes=Movie&SortBy=SortName&SortOrder=Ascending&StartIndex=6000&Limit=100&Fields=PrimaryImageAspectRatio%2CBasicSyncInfo%2CProductionYear"},
		{Name: "series p1", Class: classBrowse, Path: "/Users/{userId}/Items?ParentId={showsView}&Recursive=true&IncludeItemTypes=Series&SortBy=SortName&SortOrder=Ascending&StartIndex=0&Limit=100", Discover: discoverMBItem("seriesId", "Series")},
		{Name: "movie detail", Class: classDetail, Path: "/Users/{userId}/Items/{movieId}"},
		{Name: "series Seasons", Class: classDetail, Path: "/Shows/{seriesId}/Seasons?UserId={userId}", Discover: discoverMBItem("seasonId", "Season")},
		{Name: "season Episodes", Class: classDetail, Path: "/Shows/{seriesId}/Episodes?SeasonId={seasonId}&UserId={userId}"},
		{Name: "search", Class: classSearch, Path: "/Users/{userId}/Items?SearchTerm=star&Recursive=true&IncludeItemTypes=Movie%2CSeries&Limit=24"},
	}, embyLibrarySteps()...)}
}

func infuseEmby() Client {
	f := infuseFields
	return Client{Name: "infuse-emby", Surface: "emby", Port: 2, SharedPrefix: "/emby", Headers: mediaBrowserHeaders("Infuse-Library", "8.1", "Apple TV", true), SignIn: mediaBrowserSignIn(), Steps: []Step{
		{Name: "/Users/{id}/Views", Class: classHome, Path: "/Users/{userId}/Views", Discover: discoverViews},
		{Name: "/Users/{id}/Items/Resume", Class: classHome, Path: "/Users/{userId}/Items/Resume?fields=" + f + "&limit=40&mediaTypes=Video"},
		{Name: "movies p1 SortName", Class: classBrowse, Path: "/Users/{userId}/Items?excludeLocationTypes=Virtual&fields=" + f + "&includeItemTypes=Movie&limit=50&parentId={moviesView}&recursive=true&sortBy=SortName&sortOrder=Ascending&startIndex=0"},
	}}
}

func siloHeaders(s *session) http.Header {
	h := http.Header{}
	h.Set("Accept", "application/json")
	h.Set("Content-Type", "application/json")
	h.Set("X-Silo-Client", "silo-appletv")
	h.Set("X-Silo-Client-Version", "1.0")
	h.Set("X-Silo-Device-Id", "etherperf-silo-"+strconv.FormatInt(s.Viewer.ID, 10))
	h.Set("X-Silo-Device-Name", "etherperf")
	h.Set("X-Silo-Device-Platform", "tvos")
	// Silo apps send the chosen profile, which is the viewer.
	h.Set("X-Profile-Id", strconv.FormatInt(s.Viewer.ID, 10))
	if tok := s.vars["token"]; tok != "" {
		h.Set("Authorization", "Bearer "+tok)
	}
	return h
}

// discoverSiloContent takes the first title of kind ("movie", "series")
// from a Silo response: its content_id, whatever form the server issues.
// Every Silo detail, watch and state step needs one; a pattern for an older
// ID format matched nothing and the gate skipped them all.
func discoverSiloContent(name, kind string) func(map[string]string, []byte) {
	return func(vars map[string]string, body []byte) {
		var r any
		if json.Unmarshal(body, &r) != nil {
			return
		}
		var walk func(v any) bool
		walk = func(v any) bool {
			switch x := v.(type) {
			case map[string]any:
				if typ, _ := x["type"].(string); typ == kind {
					if id := jsonText(x["content_id"]); id != "" {
						setOnce(vars, name, id)
						return true
					}
				}
				// In key order: map order would pick a different title on
				// each run.
				for _, k := range slices.Sorted(maps.Keys(x)) {
					if walk(x[k]) {
						return true
					}
				}
			case []any:
				for _, c := range x {
					if walk(c) {
						return true
					}
				}
			}
			return false
		}
		walk(r)
	}
}

func discoverRegexp(name string, re *regexp.Regexp) func(map[string]string, []byte) {
	return func(vars map[string]string, body []byte) {
		if m := re.FindSubmatch(body); m != nil {
			setOnce(vars, name, string(m[1]))
		}
	}
}

func discoverSiloLibraries(vars map[string]string, body []byte) {
	var r any
	if json.Unmarshal(body, &r) != nil {
		return
	}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			id, _ := x["id"]
			typ, _ := x["type"].(string)
			if id != nil {
				idText := jsonText(id)
				switch typ {
				case "movie", "movies":
					setOnce(vars, "movieLibrary", idText)
				case "series", "show", "shows", "tv":
					setOnce(vars, "seriesLibrary", idText)
				}
			}
			for _, c := range x {
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	walk(r)
}

func jsonText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatInt(int64(x), 10)
	}
	return ""
}

func siloAppleTV() Client {
	img := "backdrop_width=2688&image_size=medium&logo_width=1760"
	return Client{Name: "silo-appletv", Surface: "silo", Port: 3, SharedPrefix: "/silo", Headers: siloHeaders,
		SignIn: &Step{Name: "auth/login", Class: classSignIn, Method: http.MethodPost, Path: "/api/v2/auth/login", Body: `{"username":"{username}","password":"{password}"}`,
			Discover: func(vars map[string]string, body []byte) {
				var r struct {
					AccessToken string `json:"access_token"`
				}
				if json.Unmarshal(body, &r) == nil {
					vars["token"] = r.AccessToken
				}
			}},
		Steps: append([]Step{
			{Name: "system/info", Class: classStatic, Path: "/api/v2/system/info"},
			{Name: "account/me", Class: classStatic, Path: "/api/v2/account/me"},
			{Name: "profiles", Class: classStatic, Path: "/api/v2/profiles"},
			{Name: "user/libraries", Class: classHome, Path: "/api/v2/user/libraries", Discover: discoverSiloLibraries},
			{Name: "recommendations/discover", Class: classHome, Path: "/api/v2/recommendations/discover?" + img},
			{Name: "home/sections", Class: classHome, Path: "/api/v2/home/sections?" + img + "&item_limit=20", Discover: discoverSiloContent("movieId", "movie")},
			{Name: "library movies sections", Class: classHome, Path: "/api/v2/library/{movieLibrary}/sections?" + img + "&item_limit=20"},
			{Name: "library series sections", Class: classHome, Path: "/api/v2/library/{seriesLibrary}/sections?" + img + "&item_limit=20"},
			{Name: "catalog movies title", Class: classBrowse, Path: "/api/v2/catalog?backdrop_width=2688&library_id={movieLibrary}&limit=100&match=all&poster_width=520&sort=title&source=query&type=movie", Discover: discoverSiloContent("movieId", "movie")},
			{Name: "catalog series title", Class: classBrowse, Path: "/api/v2/catalog?backdrop_width=2688&library_id={seriesLibrary}&limit=100&match=all&poster_width=520&sort=title&source=query&type=series", Discover: discoverSiloContent("seriesId", "series")},
			{Name: "catalog item movie", Class: classDetail, Path: "/api/v2/catalog/items/{movieId}?backdrop_width=2688&logo_width=1760&photo_width=400&poster_width=920"},
			{Name: "watch movie", Class: classDetail, Path: "/api/v2/watch/{movieId}"},
			{Name: "similar movie", Class: classDetail, Path: "/api/v2/recommendations/similar/{movieId}?backdrop_width=2688&limit=12&poster_width=440"},
			{Name: "series seasons", Class: classDetail, Path: "/api/v2/catalog/series/{seriesId}/seasons?include_artwork=false&poster_width=520"},
			{Name: "series season 1 episodes", Class: classDetail, Path: "/api/v2/catalog/series/{seriesId}/seasons/1/episodes?still_width=720"},
			{Name: "search people", Class: classSearch, Path: "/api/v2/catalog/people?limit=20&media_scope=video_with_episodes&q=star"},
		}, siloLibrarySteps()...)}
}

var stremioMetaID = regexp.MustCompile(`"id"\s*:\s*"((?:tmdb:|tt)[^"]+)"`)

func stremio() Client {
	return Client{Name: "stremio", Surface: "stremio", Prefix: "/stremio/{stremio}", Headers: func(*session) http.Header {
		h := http.Header{}
		h.Set("Accept", "application/json")
		return h
	}, Steps: append([]Step{
		{Name: "manifest", Class: classStatic, Path: "/manifest.json"},
		{Name: "catalog movies", Class: classBrowse, Path: "/catalog/movie/ether-movies.json", Discover: discoverRegexp("movieMeta", stremioMetaID)},
		{Name: "catalog movies skip=1000", Class: classBrowse, Path: "/catalog/movie/ether-movies/skip=1000.json"},
		{Name: "catalog movies genre", Class: classBrowse, Path: "/catalog/movie/ether-movies/genre=Action.json"},
		{Name: "catalog series", Class: classBrowse, Path: "/catalog/series/ether-series.json", Discover: discoverRegexp("seriesMeta", stremioMetaID)},
		{Name: "catalog continue", Class: classHome, Path: "/catalog/movie/ether-continue.json"},
		{Name: "catalog recent", Class: classHome, Path: "/catalog/movie/ether-recent.json"},
		{Name: "catalog recommendations", Class: classHome, Path: "/catalog/movie/ether-recommendations.json"},
		{Name: "catalog search", Class: classSearch, RowsBound: searchRowsBound, Path: "/catalog/movie/ether-movies/search=star.json"},
		{Name: "meta movie", Class: classDetail, Path: "/meta/movie/{movieMeta}.json"},
		{Name: "meta series", Class: classDetail, Path: "/meta/series/{seriesMeta}.json"},
	}, stremioLibrarySteps()...)}
}

func console() Client {
	return Client{Name: "console", Surface: "console", Prefix: "/ether/api", Headers: func(s *session) http.Header {
		h := http.Header{}
		h.Set("Accept", "application/json")
		h.Set("X-Ether-Admin-Token", s.vars["ownerToken"])
		return h
	}, Steps: append([]Step{
		{Name: "bootstrap", Class: classConsole, Path: "/bootstrap"},
		{Name: "dashboard", Class: classConsole, Path: "/dashboard"},
		{Name: "status", Class: classConsole, Path: "/status"},
		{Name: "system/load", Class: classConsole, Path: "/system/load"},
		{Name: "catalog browse movies", Class: classConsole, Path: "/catalog/browse?type=movie&offset=0&limit=48"},
		// The console browser pages by keyset cursor; a cursor page costs the
		// same at any depth, and offsets past the first page are refused.
		{Name: "catalog browse title", Class: classConsole, Path: "/catalog/browse?type=movie&sort=title&limit=48", Discover: discoverConsoleCursor},
		{Name: "catalog browse deep", Class: classConsole, Path: "/catalog/browse?type=movie&sort=title&limit=48&after={consoleCursor}"},
		{Name: "catalog facets genre", Class: classConsole, Path: "/catalog/facets?type=movie&facet=genre"},
		{Name: "catalog facets decade", Class: classConsole, Path: "/catalog/facets?type=movie&facet=decade"},
		{Name: "catalog search", Class: classConsole, Path: "/catalog/search?q=star&limit=20"},
		{Name: "viewers", Class: classConsole, Path: "/viewers"},
		{Name: "viewer home", Class: classConsole, Path: "/viewers/{viewerId}/home"},
		{Name: "insights rows", Class: classConsole, Path: "/insights/rows"},
		{Name: "insights activity", Class: classConsole, Path: "/insights/activity"},
		{Name: "collections", Class: classConsole, Path: "/collections"},
		{Name: "frontends", Class: classConsole, Path: "/frontends"},
		{Name: "layout", Class: classConsole, Path: "/layout"},
	}, consoleLibrarySteps()...)}
}

// Only keeps the clients named in names (all of them when names is empty).
func Only(cs []Client, names []string) []Client {
	if len(names) == 0 {
		return cs
	}
	want := map[string]bool{}
	for _, n := range names {
		want[strings.TrimSpace(n)] = true
	}
	var keep []Client
	for _, c := range cs {
		if want[c.Name] {
			keep = append(keep, c)
		}
	}
	return keep
}

// OnlySteps keeps the steps whose name contains one of names (case
// insensitive); clients left with no steps are dropped. Sign-in always runs.
func OnlySteps(cs []Client, names []string) []Client {
	if len(names) == 0 {
		return cs
	}
	var out []Client
	for _, c := range cs {
		var steps []Step
		for _, st := range c.Steps {
			for _, n := range names {
				if n = strings.TrimSpace(n); n != "" && strings.Contains(strings.ToLower(st.Name), strings.ToLower(n)) {
					steps = append(steps, st)
					break
				}
			}
		}
		if len(steps) > 0 {
			c.Steps = steps
			out = append(out, c)
		}
	}
	return out
}

// queryEscape is used for credentials placed in a path.
func queryEscape(s string) string { return url.PathEscape(s) }
