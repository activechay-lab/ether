package frontend

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"
)

// LiveTVProvider is the EPG provider identifier reported on Plex Live TV routes.
const LiveTVProvider = "tv.plex.providers.epg.xmltv:1"

// RasterOptions configures an SVG-to-PNG/JPEG rasterization.
type RasterOptions struct {
	Width, Height int
	Format        string
	Blur          bool
}

// SVGRasterizer rasterizes SVG bytes or generated channel logos.
type SVGRasterizer interface {
	Rasterize(ctx context.Context, reference string, data []byte, opts RasterOptions) ([]byte, string, error)
	ChannelLogo(ctx context.Context, reference, number, name string, opts RasterOptions) ([]byte, string, error)
}

// UntrustedImageFetcher fetches an untrusted remote image URL.
type UntrustedImageFetcher interface {
	GetUntrustedURL(ctx context.Context, rawURL string) ([]byte, string, error)
}

// ArtworkRemoteImage is a fetched remote artwork payload.
type ArtworkRemoteImage struct {
	URL         string
	Data        []byte
	ContentType string
	FetchedAt   time.Time
}

// ArtworkImageCache is the remote/transformed image cache used by Plex handlers.
type ArtworkImageCache interface {
	Get(ctx context.Context, u string, now time.Time) (*ArtworkRemoteImage, error)
	RasterFormat(ctx context.Context, data []byte, ct, format string, now time.Time) ([]byte, string, error)
	IsNotFound(err error) bool
}

// ArtworkPalette holds four-corner hex colors (without leading '#').
type ArtworkPalette struct {
	TopLeft, TopRight, BottomLeft, BottomRight string
}

// ArtworkPaletteWant describes a background palette preload request.
type ArtworkPaletteWant struct {
	Key  RatingKey
	Path string
}

// ArtworkCollectionArt is generated collection artwork used for smart-collection palettes.
type ArtworkCollectionArt interface {
	CollectionArtwork(ctx context.Context, viewer *User, key RatingKey, element string, width int) ([]byte, string, bool, error)
}

// ArtworkPaletteCache is the persistent title palette store used by Plex UltraBlur.
type ArtworkPaletteCache interface {
	Lookup(key RatingKey, path string) *ArtworkPalette
	Preload(ctx context.Context, wants []ArtworkPaletteWant)
	PersistFor(persist func(context.Context, RatingKey) bool)
}

// ArtworkSourceConfig configures a host-backed ArtworkSource.
type ArtworkSourceConfig struct {
	Catalog          StoredTitleLookup
	CollectionDevice string
	Collections      Collections
	Playlists        Playlists
	Visibility       Visibility
	LiveTV           LiveTVHost
	Discovery        *DiscoveryFeatures
	Images           ArtworkImageCache
	ImageURL         ImageURLFunc
	Palettes         ArtworkPaletteCache
	Now              func() time.Time
	Variants         func(*Title, string) (*ArtworkImages, bool)
	Metadata         func(context.Context, ArtworkRequest, Image) Image
	VirtualSeason    func(context.Context, *User, RatingKey) (*Title, error)
	URLImages        UntrustedImageFetcher
	Raster           SVGRasterizer
	Generated        any
}

// ArtworkSource serves canonical artwork listings, reads, warming, and selection paths.
type ArtworkSource interface {
	Artwork
	Warm(t Title, d Device)
	ChannelImage(viewer *Viewer, channel LiveTVChannel) Image
	ArtPath(ctx context.Context, req ArtworkRequest, element string) (path string, defWidth int, err error)
}

// PlexArtworkStores constructs artwork caches/sources and exposes host-owned
// artwork and Live TV helpers to internal/plex.
type PlexArtworkStores interface {
	NewRemoteCache(budget int64, client *http.Client) ArtworkImageCache
	NewUntrustedImages(client *http.Client) UntrustedImageFetcher
	NewSVGRasterizer() SVGRasterizer
	NewPaletteCache(db *sql.DB, submit func(string, func(context.Context)) bool, now func() time.Time, log *slog.Logger, compute func(context.Context, RatingKey, string) (ArtworkPalette, error)) ArtworkPaletteCache
	ComputeItemPalette(ctx context.Context, cache ArtworkImageCache, imageURL ImageURLFunc, key RatingKey, path string, now time.Time, art ArtworkCollectionArt) (ArtworkPalette, error)
	NewSource(cfg ArtworkSourceConfig) ArtworkSource
	ResizePoster(data []byte, width int) ([]byte, error)
	TitlePosterStamp(t *Title) (string, bool)
	RenderTitlePoster(ctx context.Context, generated any, t *Title, width int) ([]byte, string, bool, error)
	DiagnoseLiveTV(stage string, err error) LiveTVTuneDiagnosis
	VirtualDASHError(err error) (message string, cause error, ok bool)
	ShowcaseLogo(c LiveTVChannel) (repoPath string, png []byte, ok bool)
}

// LiveTVItem is the frontend boundary representation of a scheduled programme's item metadata.
type LiveTVItem struct {
	Key          RatingKey
	Title        string
	EpisodeTitle string
	ShowTitle    string
	Summary      string
	Rating       string
	Thumb        string
	Art          string
	Genres       []string
	GUID         string
	ShowGUID     string
	MediaType    string
	Premiere     bool
	Live         bool
	Season       int
	Episode      int
	ReleasedMS   int64
}

// LiveTVChannel is the frontend boundary representation of a Live TV channel.
type LiveTVChannel struct {
	ID            string
	Number        string
	Name          string
	Kind          string
	Enabled       bool
	Logo          string
	LogoMatched   bool
	Rating        string
	SourceID      string
	GuideID       string
	GuideSourceID string
	Group         string
	Personal      bool
}

// LiveTVProgramme is the frontend boundary representation of a guide airing.
type LiveTVProgramme struct {
	ID        string
	ChannelID string
	Start     time.Time
	End       time.Time
	Lens      string
	Item      LiveTVItem
}

// LiveTVSessionInfo is the frontend boundary representation of an active Live TV tune session.
type LiveTVSessionInfo struct {
	ID             string
	SubscriptionID int64
	ActivityID     string
	MediaInfo      *MediaInfo
	Channel        LiveTVChannel
	Programme      *LiveTVProgramme
	StartedAt      time.Time
}

// LiveTVTuneDiagnosis is a structured classification of a Live TV tune or playback error.
type LiveTVTuneDiagnosis struct {
	Code           string
	Stage          string
	UpstreamStatus int
	RetryAfter     time.Duration
	Message        string
	LogAttrs       []any
}

// LiveTVHost is the host-owned Live TV service interface consumed by internal/plex.
type LiveTVHost interface {
	VisibleTo(ctx context.Context, u *User) bool
	ServeXMLTV(w http.ResponseWriter, r *http.Request, u *User)
	Stop(ctx context.Context, id string, u *User) error
	ServeDASHPart(w http.ResponseWriter, r *http.Request, id string, u *User, rep, n int, init bool)
	ServePlaylist(w http.ResponseWriter, r *http.Request, id string, u *User)
	ServeRelaySegment(w http.ResponseWriter, r *http.Request, id string, n int, u *User)
	ServeResource(w http.ResponseWriter, r *http.Request, id, resource string, u *User)
	AllowedChannels(ctx context.Context, u *User) ([]LiveTVChannel, error)
	AllowedChannel(ctx context.Context, u *User, id string) (LiveTVChannel, error)
	Channel(ctx context.Context, id string) (LiveTVChannel, error)
	ChannelForViewer(ctx context.Context, u *User, id string) (LiveTVChannel, error)
	Channels(ctx context.Context) ([]LiveTVChannel, error)
	ChannelPermitted(ctx context.Context, u *User, id string) (bool, error)
	LineupChangedFor(u *User) time.Time
	GuideForChannels(ctx context.Context, u *User, from, until time.Time, channelIDs []string) ([]LiveTVProgramme, error)
	Guide(ctx context.Context, u *User, from, until time.Time) ([]LiveTVProgramme, error)
	GuideChannelLogo(ctx context.Context, c LiveTVChannel) string
	TuneWithSession(ctx context.Context, u *User, id string, caps ClientCaps, sessionID string) (LiveTVSessionInfo, error)
	Sessions(ctx context.Context, u *User) []LiveTVSessionInfo
	Session(ctx context.Context, id string, u *User) (LiveTVSessionInfo, error)
	ServeDASH(w http.ResponseWriter, r *http.Request, id string, u *User) error
	ServeHTTP(w http.ResponseWriter, r *http.Request, id string, u *User) error
	ViewingSessions() []*PlaySession
	NoteClientActivity()
	AuthenticateMediaGrant(r *http.Request, active func(context.Context, int64) *User) (*User, bool)
}
