package app

import "github.com/braedonsaunders/ether/internal/extend"

// registerConsoleDescriptors registers built-in pages, navigation, commands
// and integration cards (WS1d). The console derives the sidebar, ⌘K "Go to"
// and actions, the sitemap and the Integrations cards from these.
func (a *App) registerConsoleDescriptors(r *extend.Registry) error {
	return r.Register(consoleDescriptors()...)
}

// consoleDescriptors is the built-in console catalogue. Order, icons and
// labels match the hand-written console lists this replaced.
func consoleDescriptors() []extend.Descriptor {
	var out []extend.Descriptor
	for _, p := range consolePages() {
		d := extend.Descriptor{
			Kind: extend.KindPage, ID: p.id, Owner: extend.Builtin,
			Label: p.label, Icon: p.icon, Group: p.meta.NavGroup, Order: p.order,
		}.WithMeta(p.meta)
		out = append(out, d)
		if p.meta.InNav {
			out = append(out, extend.Descriptor{
				Kind: extend.KindNavItem, ID: p.id, Owner: extend.Builtin,
				Label: p.label, Icon: p.icon, Group: p.meta.NavGroup, Order: p.order,
			}.WithMeta(extend.NavItemMeta{Route: p.meta.Route, Page: p.id, Mobile: p.meta.Mobile, Exact: p.meta.Exact}))
		}
	}
	out = append(out, consoleCommands()...)
	out = append(out, consoleIntegrations()...)
	return out
}

type consolePage struct {
	id    string
	label extend.Text
	icon  string
	order int
	meta  extend.PageMeta
}

func section(key string, label, keywords extend.Text) extend.PageSection {
	return extend.PageSection{Key: key, Label: label, Keywords: keywords}
}

func consolePages() []consolePage {
	T := extend.T
	return []consolePage{
		{id: "overview", label: T("Overview"), icon: "Gauge", order: 10, meta: extend.PageMeta{
			Route: "/", InNav: true, Mobile: true, Exact: true,
			Keywords: T("home dashboard health attention status"),
		}},
		{id: "activity", label: T("Activity"), icon: "Activity", order: 20, meta: extend.PageMeta{
			Route: "/activity", InNav: true, Mobile: true,
			Sections: []extend.PageSection{
				section("now", T("Now playing"), T("streams sessions stop live")),
				section("history", T("History"), T("plays watched")),
				section("stats", T("Stats"), T("statistics devices hours viewers titles")),
			},
		}},
		{id: "viewers", label: T("Viewers"), icon: "Users", order: 30, meta: extend.PageMeta{
			Route: "/viewers", InNav: true, Mobile: true,
			Keywords: T("users friends accounts people"),
		}},
		{id: "libraries", label: T("Libraries"), icon: "Library", order: 10, meta: extend.PageMeta{
			Route: "/libraries", NavGroup: extend.NavGroupLibrary, InNav: true,
			Keywords: T("catalogue catalog movies shows browse search library libraries"),
			Sections: []extend.PageSection{
				section("settings", T("Library settings"), T("tmdb language region popularity new releases playable digital")),
			},
		}},
		{id: "collections", label: T("Collections"), icon: "Layers", order: 20, meta: extend.PageMeta{
			Route: "/collections", NavGroup: extend.NavGroupLibrary, InNav: true, Mobile: true,
			Keywords: T("studio templates rows"),
		}},
		{id: "home", label: T("Home"), icon: "LayoutTemplate", order: 30, meta: extend.PageMeta{
			Route: "/home", NavGroup: extend.NavGroupLibrary, InNav: true,
			Keywords: T("designer rows layout overrides plex home"),
		}},
		{id: "livetv", label: T("Live TV"), icon: "Tv", order: 40, meta: extend.PageMeta{
			Route: "/livetv", NavGroup: extend.NavGroupLibrary, InNav: true,
			Sections: []extend.PageSection{
				section("channels", T("Channels"), T("virtual channel")),
				section("sources", T("IPTV sources"), T("m3u xmltv playlist")),
				section("guide", T("Guide"), T("epg schedule")),
				section("looks", T("Looks"), T("on air graphics brand reels")),
			},
		}},
		{id: "calendar", label: T("Calendar"), icon: "CalendarDays", order: 50, meta: extend.PageMeta{
			Route: "/calendar", NavGroup: extend.NavGroupLibrary, InNav: true,
			Keywords: T("upcoming episodes premieres finales releases airing tonight sonarr radarr schedule"),
			Sections: []extend.PageSection{
				section("month", T("Month"), T("schedule grid")),
				section("week", T("Week"), extend.Text{}),
				section("agenda", T("Agenda"), T("list upcoming")),
			},
		}},
		{id: "metadata", label: T("Metadata"), icon: "Tags", order: 60, meta: extend.PageMeta{
			Route: "/metadata", NavGroup: extend.NavGroupLibrary, InNav: true,
			Sections: []extend.PageSection{
				section("ratings", T("Ratings"), T("omdb mdblist imdb rotten badges")),
				section("artwork", T("Artwork"), T("posters overlays backdrop")),
				section("episodes", T("Episode order"), T("dvd absolute aired ordering")),
			},
		}},
		{id: "sources", label: T("Sources"), icon: "Network", order: 10, meta: extend.PageMeta{
			Route: "/sources", NavGroup: extend.NavGroupStreaming, InNav: true,
			Sections: []extend.PageSection{
				section("debrid", T("Debrid"), T("real-debrid realdebrid torbox alldebrid premiumize debrid-link debridlink offcloud torrent cached")),
				section("usenet", T("Usenet"), T("nntp backfill connections")),
				section("indexers", T("Indexers"), T("newznab torznab torrent built-in sites definitions cardigann flaresolverr prowlarr stremio addon budget api")),
				section("stats", T("Stats"), T("quota price plan cost success speed plays failover")),
			},
		}},
		{id: "releases", label: T("Quality"), icon: "ListFilter", order: 20, meta: extend.PageMeta{
			Route: "/quality", NavGroup: extend.NavGroupStreaming, InNav: true,
			Sections: []extend.PageSection{
				section("profiles", T("Profiles"), T("quality profiles resolution 4k 1080p remote cam scoring trash custom formats rules ranking")),
				section("devices", T("Device rules"), T("device client profile")),
				section("searching", T("Searching"), T("how ether searches versions cache health samples candidates")),
				section("blocklist", T("Blocklist"), T("blocked bad release unblock terms groups")),
				section("pinned", T("Pinned"), T("pin release override search interactive manual pick find")),
				section("history", T("History"), T("traces resolver why release debug decisions")),
			},
		}},
		{id: "cache", label: T("Cache"), icon: "HardDrive", order: 30, meta: extend.PageMeta{
			Route: "/cache", NavGroup: extend.NavGroupStreaming, InNav: true,
			Keywords: T("disk budget storage retention prefetch warming next episode"),
			Sections: []extend.PageSection{
				section("overview", T("Overview"), T("storage readiness policy")),
				section("explorer", T("Explorer"), T("retained media releases pins")),
				section("preloading", T("Preloading"), T("household selection popular movies tv warm arrivals")),
			},
		}},
		{id: "playback", label: T("Playback"), icon: "MonitorPlay", order: 15, meta: extend.PageMeta{
			Route: "/playback", NavGroup: extend.NavGroupStreaming, InNav: true,
			Sections: []extend.PageSection{
				section("transcoding", T("Transcoding"), T("ffmpeg hardware gpu bitrate startup race health")),
				section("devices", T("App compatibility"), T("compatibility codecs")),
				section("markers", T("Skip buttons"), T("intro credits skip chapters")),
				section("subtitles", T("Audio & subtitles"), T("audio languages subtitles timing sync opensubtitles household")),
			},
		}},
		{id: "general", label: T("General"), icon: "Server", order: 10, meta: extend.PageMeta{
			Route: "/server", NavGroup: extend.NavGroupServer, InNav: true,
			Keywords: T("settings"),
			Sections: []extend.PageSection{
				section("server", T("Server"), T("name port listen log level stream limit")),
				section("downloads", T("Downloads"), T("offline sync friends remux")),
				section("personalize", T("Personalization"), T("post-play up next match finish time clock")),
				section("extras", T("Trailers & extras"), T("cinema trailers yt-dlp resolver")),
			},
		}},
		{id: "apps", label: T("Apps"), icon: "Globe", order: 20, meta: extend.PageMeta{
			Route: "/apps", NavGroup: extend.NavGroupServer, InNav: true,
			Keywords: T("settings plex apps protocols"),
			Sections: []extend.PageSection{
				section("remote", T("Remote access"), T("upnp port forward public")),
				section("secure", T("Secure connections"), T("certificate plex.direct tls")),
			},
		}},
		{id: "integrations", label: T("Integrations"), icon: "Plug", order: 30, meta: extend.PageMeta{
			Route: "/services", NavGroup: extend.NavGroupServer, InNav: true,
			Keywords: T("settings integrations"),
			Sections: []extend.PageSection{
				section("overview", T("Integrations"), T("status tmdb debrid prowlarr ratings")),
				section("trakt", T("Trakt"), T("sync history")),
				section("simkl", T("Simkl"), T("simkl sync history")),
				section("llm", T("Language model"), T("llm ollama openai recaps search")),
				section("opensubtitles", T("OpenSubtitles"), T("subtitles")),
			},
		}},
		{id: "plugins", label: T("Plugins"), icon: "Puzzle", order: 40, meta: extend.PageMeta{
			Route: "/plugins", NavGroup: extend.NavGroupServer, InNav: true,
			Keywords: T("plugins extensions addons studio"),
		}},
		{id: "notifications", label: T("Notifications"), icon: "Bell", order: 50, meta: extend.PageMeta{
			Route: "/notifications", NavGroup: extend.NavGroupServer, InNav: true,
			Sections: []extend.PageSection{
				section("channels", T("Channels"), T("email discord webhook push alerts watchlist")),
				section("report", T("Weekly report"), T("newsletter digest")),
				section("deliveries", T("Delivery history"), extend.Text{}),
			},
		}},
		{id: "cluster", label: T("Cluster"), icon: "Boxes", order: 60, meta: extend.PageMeta{
			Route: "/cluster", NavGroup: extend.NavGroupServer, InNav: true,
			Keywords: T("high availability ha nodes failover postgres postgresql join token weight leader"),
		}},
		{id: "maintenance", label: T("Maintenance"), icon: "LifeBuoy", order: 70, meta: extend.PageMeta{
			Route: "/maintenance", NavGroup: extend.NavGroupServer, InNav: true,
			Keywords: T("server maintenance"),
		}},
		{id: "backups", label: T("Backups & import"), order: 71, meta: extend.PageMeta{
			Route: "/maintenance/backups",
			Sections: []extend.PageSection{
				section("backups", T("Backups"), T("snapshot restore schedule")),
				section("import", T("Import & export"), T("migrate plex csv letterboxd trakt")),
			},
		}},
		{id: "updates", label: T("Updates"), order: 72, meta: extend.PageMeta{
			Route:    "/maintenance/updates",
			Keywords: T("version channel beta maintenance window update now install"),
		}},
		{id: "troubleshooting", label: T("Troubleshooting"), order: 73, meta: extend.PageMeta{
			Route:    "/maintenance/troubleshooting",
			Keywords: T("local file test playback"),
		}},
		{id: "logs", label: T("Logs"), order: 74, meta: extend.PageMeta{
			Route:    "/maintenance/logs",
			Keywords: T("errors debug support zip record a problem"),
		}},
	}
}

func consoleCommands() []extend.Descriptor {
	T := extend.T
	type cmd struct {
		id    string
		label extend.Text
		order int
		meta  extend.CommandMeta
	}
	cmds := []cmd{
		{id: "record-problem", label: T("Record a problem"), order: 10, meta: extend.CommandMeta{
			Route: "/maintenance/logs?record=1", Keywords: T("support zip diagnostic capture")}},
		{id: "add-indexer", label: T("Add an indexer"), order: 20, meta: extend.CommandMeta{
			Route: "/sources/indexers", Keywords: T("newznab prowlarr")}},
		{id: "add-usenet-provider", label: T("Add a Usenet provider"), order: 30, meta: extend.CommandMeta{
			Route: "/sources/usenet", Keywords: T("nntp")}},
		{id: "add-debrid-provider", label: T("Add a debrid provider"), order: 40, meta: extend.CommandMeta{
			Route: "/sources/debrid", Keywords: T("real-debrid torbox alldebrid premiumize debrid-link debridlink offcloud torrent")}},
		{id: "new-collection", label: T("New collection"), order: 50, meta: extend.CommandMeta{
			Route: "/collections/new", Keywords: T("create")}},
		{id: "new-notification-channel", label: T("New notification channel"), order: 60, meta: extend.CommandMeta{
			Route: "/notifications/channels", Keywords: T("alert")}},
		{id: "back-up-now", label: T("Back up now"), order: 70, meta: extend.CommandMeta{
			Route: "/maintenance/backups", Keywords: T("backup snapshot")}},
		// Ask Ether suggestions (WS6): the label is the suggestion text.
		{id: "ask.releases.why-pick", label: T("Why did this title pick this release?"), order: 110, meta: extend.CommandMeta{
			AgentSuggestion: true, RoutePatterns: []string{"/quality", "/quality/*"}}},
		{id: "ask.livetv.failing", label: T("Which channels are failing?"), order: 120, meta: extend.CommandMeta{
			AgentSuggestion: true, RoutePatterns: []string{"/livetv", "/livetv/*"}}},
		{id: "ask.sources.add-stremio", label: T("Add a plugin from Stremio"), order: 130, meta: extend.CommandMeta{
			AgentSuggestion: true, RoutePatterns: []string{"/sources", "/sources/*"}}},
		{id: "ask.activity.buffering", label: T("Why did playback buffer?"), order: 140, meta: extend.CommandMeta{
			AgentSuggestion: true, RoutePatterns: []string{"/activity", "/activity/*"}}},
	}
	out := make([]extend.Descriptor, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, extend.Descriptor{
			Kind: extend.KindCommand, ID: c.id, Owner: extend.Builtin, Label: c.label, Order: c.order,
		}.WithMeta(c.meta))
	}
	return out
}

func consoleIntegrations() []extend.Descriptor {
	T := extend.T
	type integration struct {
		id    string
		label extend.Text
		hint  extend.Text
		order int
		meta  extend.IntegrationMeta
	}
	items := []integration{
		{id: "tmdb", label: T("TMDB"), order: 10,
			hint: T("Names titles, fetches artwork and fills the catalogue."),
			meta: extend.IntegrationMeta{
				Category: "required", Logo: "tmdb", Connect: "fields",
				Tab: "tmdb", ConfigKey: "catalog", Endpoint: "catalog", Test: "tmdb",
				ReadyField: "tmdbKey",
				Adds:       []extend.Text{T("Catalogue"), T("Artwork")},
			}},
		{id: "trakt", label: T("Trakt"), order: 10,
			hint: T("Sync watch history and lists with Trakt."),
			meta: extend.IntegrationMeta{
				Category: "watch-tracking", Logo: "trakt", Connect: "fields",
				Tab: "trakt", ConfigKey: "trakt", Endpoint: "trakt", Test: "trakt",
				Flag: "enabled", RequiredField: "clientId",
				Adds: []extend.Text{T("Watch history"), T("Lists")},
			}},
		{id: "simkl", label: T("Simkl"), order: 20,
			hint: T("Connect a viewer's Simkl account to import watched history and send completed playback."),
			meta: extend.IntegrationMeta{
				Category: "watch-tracking", Logo: "simkl", Connect: "custom", Form: "simkl",
				Tab: "simkl", ConfigKey: "simkl", Endpoint: "simkl",
				Flag: "enabled", RequiredField: "clientId",
				Adds: []extend.Text{T("Watch history")},
			}},
		{id: "ratings", label: T("OMDb and MDBList"), order: 90,
			hint: T("IMDb, Rotten Tomatoes and other rating badges."),
			meta: extend.IntegrationMeta{
				Category: "artwork-ratings", Logo: "ratings", Connect: "custom", Form: "ratings",
				Tab: "ratings", RequiredField: "apiKey",
				Adds: []extend.Text{T("Rating badges")},
			}},
		{id: "opensubtitles", label: T("OpenSubtitles"), order: 10,
			hint: T("Fetch subtitles when a release doesn't include them."),
			meta: extend.IntegrationMeta{
				Category: "playback-extras", Logo: "opensubtitles", Connect: "fields",
				Tab: "opensubtitles", ConfigKey: "openSubtitles", Endpoint: "opensubtitles", Test: "opensubtitles",
				Flag: "enabled", ReadyField: "apiKey",
				Adds: []extend.Text{T("Subtitles")},
			}},
		{id: "introdb", label: T("TheIntroDB"), order: 20,
			hint: T("Intro and credits markers so viewers can skip them."),
			meta: extend.IntegrationMeta{
				Category: "playback-extras", Logo: "introdb", Connect: "fields",
				Tab: "introdb", ConfigKey: "markers", Endpoint: "markers", Test: "introdb",
				Flag: "theIntroDb", ReadyField: "apiKey", PublicFallback: true,
				Adds: []extend.Text{T("Skip buttons")},
			}},
		{id: "llm", label: T("Language model"), order: 10,
			hint: T("Describe-it collections, themed Home rows, natural-language search and recaps."),
			meta: extend.IntegrationMeta{
				Category: "smart-features", Logo: "llm", Connect: "custom", Form: "llm",
				Tab: "llm", ConfigKey: "llm", Endpoint: "llm", Test: "llm",
				Flag: "enabled", RequiredField: "model",
				Adds: []extend.Text{T("Collections"), T("Search"), T("Recaps")},
			}},
	}
	out := make([]extend.Descriptor, 0, len(items))
	for _, it := range items {
		out = append(out, extend.Descriptor{
			Kind: extend.KindIntegration, ID: it.id, Owner: extend.Builtin,
			Label: it.label, Hint: it.hint, Group: it.meta.Category, Order: it.order,
		}.WithMeta(it.meta))
	}
	return out
}
