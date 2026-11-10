package frontend

import (
	"context"
	"database/sql"
	"net/http"
)

// PlexSection is a library section exposed to Plex clients.
type PlexSection struct {
	ID       int
	Type     MediaType // movie or show
	Title    string
	UUID     string // stable; derived from the machine identifier when empty
	Language string
}

// PlexDeps are the Plex server's dependencies. Host supplies generic frontend
// services; remaining optional fields degrade gracefully when nil.
type PlexDeps struct {
	Host       Host
	Playback   Playback
	Transcoder Transcoder

	// Sections overrides the default Movies (1) and TV Shows (2) sections.
	Sections []PlexSection
	// DB persists artwork data and ordered shelf-generation keys. Clustered
	// nodes share this store so continuations survive failover and restarts.
	// Optional: without it, these caches remain process-local.
	DB *sql.DB
	// DisableEnrichment turns off the background title-detail and
	// UltraBlurColors work (the spike test library has nothing to fetch).
	DisableEnrichment bool
	// TracePlayback logs HLS/transcode requests when ETHER_TRACE_PLAYBACK=1.
	// It does not change responses.
	TracePlayback bool
}

// PlexServer is the host-visible contract of a constructed Plex server.
type PlexServer interface {
	http.Handler
	Close() error
	Closed() bool
	Done() <-chan struct{}
	ViewerHomeChanged(userID int64)
	NotifyLiveLineup()
	LibrarySections() []PlexSection
}

// PlexHost supplies Plex's host services and lifecycle hooks on Host.
type PlexHost interface {
	Server(ctx context.Context) (PlexServer, bool, error)
	Deps(ctx context.Context) (PlexDeps, error)
	BindServer(PlexServer)
	WatchRefresh(fn func(userID int64, key RatingKey)) func()
	Status(ctx context.Context) Status
	Listeners() []ListenerSpec
}
