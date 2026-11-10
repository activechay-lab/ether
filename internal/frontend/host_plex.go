package frontend

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"time"
)

// PlexSection is a library section exposed to Plex clients.
type PlexSection struct {
	ID       int
	Type     MediaType // movie or show
	Title    string
	UUID     string // stable; derived from the machine identifier when empty
	Language string
}

// SegmentedEncoder is an opt-in trusted host seam implemented by the shared broker.
// Starts occur only after existing Plex authentication, owner gates, preparation
// and admission. The broker must revalidate the exact source revision and retain
// uncertain execution responsibility. It may choose local execution only before
// remote allocation; a returned error is never permission for Plex to retry local.
// Session/Stop true means the broker owns responsibility, even when Session is
// nil because start is pending. Transport errors must never turn true into false.
type SegmentedEncoder interface {
	StartSegmentedHLS(context.Context, *User, string, string, HLSRequest) (HLSSession, error)
	SegmentedHLSSession(string) (HLSSession, bool)
	StopSegmentedHLS(string) bool
}

// SubtitleSource resolves cached OpenSubtitles and synchronized sidecars.
type SubtitleSource interface {
	OpenFormat(string, string) (io.ReadCloser, string, error)
}

// SubtitleSidecars is optionally implemented by PlexDeps.Subtitles: the sidecars
// already cached for a title and viewer, without network access, skipping
// streams embedded already lists (by ExternalURL) or covers (by language).
type SubtitleSidecars interface {
	CachedSidecars(ctx context.Context, t *Title, u *User, embedded []StreamInfo) []StreamInfo
}

// SidecarOffer is one subtitle search result.
type SidecarOffer struct {
	ID              int64  // provider file id, passed back to DownloadSubtitle
	Language        string // ISO 639-1
	Title           string // release or file name
	Provider        string // "OpenSubtitles"
	Score           int
	HearingImpaired bool
	Forced          bool
	PerfectMatch    bool // matched the playing file's hash or release
}

// SubtitleSearch is optionally implemented by PlexDeps.Subtitles: Plex's
// on-demand subtitle search and pick.
type SubtitleSearch interface {
	SearchSubtitles(ctx context.Context, t *Title, u *User, lang, release string) ([]SidecarOffer, error)
	// DownloadSubtitle fetches an offer returned by SearchSubtitles for t
	// and returns its sidecar stream; ErrNotFound for unknown offers.
	DownloadSubtitle(ctx context.Context, t *Title, u *User, id int64) (StreamInfo, error)
}

// SubtitleUpload is optionally implemented by PlexDeps.Subtitles: Plex Web's
// "Upload Subtitles" (POST /library/metadata/{key}/subtitles).
type SubtitleUpload interface {
	UploadSubtitle(ctx context.Context, t *Title, u *User, filename, format, language string, body []byte) (StreamInfo, error)
}

// PlexDeps are the Plex server's dependencies. Host supplies generic frontend
// services; remaining session/media fields degrade gracefully when nil.
type PlexDeps struct {
	Host Host
	// Encoder optionally dispatches authenticated universal playback through
	// the shared broker; nil preserves the existing local encoder path.
	Encoder SegmentedEncoder
	Streams StreamBindings
	// SessionStores constructs the session stores this server owns and
	// provides the session helpers it calls. Required: NewServer panics
	// without it. The app wires sessions.NewSessionStores().
	SessionStores       SessionStores
	SessionCoordination Coordination
	LibraryGUIDs        LibraryGUIDs
	LibraryMetadata     LibraryMetadata
	Playback            Playback
	Transcoder          Transcoder
	// Variants lists distinct release versions for "Play Version" detail
	// pages (optional: without it, detail pages stay single-Media).
	Variants VariantResolver
	// DecisionTraces is the latest resolution for a title, copied into a
	// support capture. Nil leaves playback unchanged.
	DecisionTraces DecisionTraceSource
	// Downloads remuxes resolved sources to temp MP4s for the Download
	// Queue API; nil disables downloads (allowSync stays "0").
	Downloads        DownloadRemuxer
	TrackPreferences PlaybackTrackPreferences
	Markers          MarkerRanges
	Subtitles        SubtitleSource
	Extras           ExtrasProvider
	// Prerolls are owner pre-rolls (#298); nil leaves play queues and
	// extras exactly as without the feature.
	Prerolls  PrerollService
	Trickplay TrickplayProvider

	// Sections overrides the default Movies (1) and TV Shows (2) sections.
	Sections []PlexSection
	// DB persists artwork data and ordered shelf-generation keys. Clustered
	// nodes share this store so continuations survive failover and restarts.
	// Optional: without it, these caches remain process-local.
	DB *sql.DB
	// DisableEnrichment turns off the background title-detail and
	// UltraBlurColors work (the spike test library has nothing to fetch).
	DisableEnrichment bool
	// Cluster, when set (clustered nodes only; requires DB), shares play
	// sessions, play queues, terminations and notifications with the other
	// nodes. Nil keeps everything in this process.
	Cluster ClusterNode
	// SessionHeartbeat is how often a clustered node refreshes its shared
	// play sessions (default 10s).
	SessionHeartbeat time.Duration
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
