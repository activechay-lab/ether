package frontend

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/artwork"
)

// RasterOptions configures an SVG-to-PNG/JPEG rasterization.
type RasterOptions = artwork.RasterOptions

// TitlePoster describes an Ether-generated fallback poster for a title.
type TitlePoster = artwork.TitlePoster

// TitlePosterRenderer renders Ether's fallback poster for a title with no artwork.
type TitlePosterRenderer interface {
	Title(ctx context.Context, p TitlePoster) ([]byte, string, error)
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

// ArtworkImageCache is the remote/transformed image cache used by frontend handlers.
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

// ArtworkPaletteCache is the persistent title palette store used by UltraBlur.
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
	LiveTV           LiveTV
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
	Generated        TitlePosterRenderer
}

// ArtworkSource serves canonical artwork listings, reads, warming, and selection paths.
type ArtworkSource interface {
	Artwork
	Warm(t Title, d Device)
	ChannelImage(viewer *Viewer, channel LiveTVChannel) Image
	ArtPath(ctx context.Context, req ArtworkRequest, element string) (path string, defWidth int, err error)
}

var (
	newRemoteCacheFunc   func(budget int64, client *http.Client) ArtworkImageCache
	newPaletteCacheFunc  func(db *sql.DB, submit func(string, func(context.Context)) bool, now func() time.Time, log *slog.Logger, compute func(context.Context, RatingKey, string) (ArtworkPalette, error)) ArtworkPaletteCache
	computePaletteFunc   func(ctx context.Context, cache ArtworkImageCache, imageURL ImageURLFunc, key RatingKey, path string, now time.Time, art ArtworkCollectionArt) (ArtworkPalette, error)
	newArtworkSourceFunc func(cfg ArtworkSourceConfig) ArtworkSource
)

// RegisterArtworkStores wires host artwork store constructors for frontends.
func RegisterArtworkStores(
	newRemoteCache func(budget int64, client *http.Client) ArtworkImageCache,
	newPaletteCache func(db *sql.DB, submit func(string, func(context.Context)) bool, now func() time.Time, log *slog.Logger, compute func(context.Context, RatingKey, string) (ArtworkPalette, error)) ArtworkPaletteCache,
	computePalette func(ctx context.Context, cache ArtworkImageCache, imageURL ImageURLFunc, key RatingKey, path string, now time.Time, art ArtworkCollectionArt) (ArtworkPalette, error),
	newSource func(cfg ArtworkSourceConfig) ArtworkSource,
) {
	newRemoteCacheFunc = newRemoteCache
	newPaletteCacheFunc = newPaletteCache
	computePaletteFunc = computePalette
	newArtworkSourceFunc = newSource
}

// NewArtworkRemoteCache constructs an in-memory remote artwork cache.
func NewArtworkRemoteCache(budget int64, client *http.Client) ArtworkImageCache {
	if newRemoteCacheFunc != nil {
		return newRemoteCacheFunc(budget, client)
	}
	return nil
}

// NewUntrustedImageFetcher constructs an untrusted remote image fetcher.
func NewUntrustedImageFetcher(client *http.Client) UntrustedImageFetcher {
	return artwork.NewImages(artwork.ImagesOptions{HTTPClient: client})
}

// NewSVGRasterizer constructs an SVG rasterizer.
func NewSVGRasterizer() SVGRasterizer {
	return artwork.NewSVGRasterizer()
}

// NewArtworkPaletteCache constructs a title palette cache.
func NewArtworkPaletteCache(db *sql.DB, submit func(string, func(context.Context)) bool, now func() time.Time, log *slog.Logger, compute func(context.Context, RatingKey, string) (ArtworkPalette, error)) ArtworkPaletteCache {
	if newPaletteCacheFunc != nil {
		return newPaletteCacheFunc(db, submit, now, log, compute)
	}
	return nil
}

// ComputeArtworkItemPalette computes the four-corner palette for an item.
func ComputeArtworkItemPalette(ctx context.Context, cache ArtworkImageCache, imageURL ImageURLFunc, key RatingKey, path string, now time.Time, art ArtworkCollectionArt) (ArtworkPalette, error) {
	if computePaletteFunc != nil {
		return computePaletteFunc(ctx, cache, imageURL, key, path, now, art)
	}
	return ArtworkPalette{}, ErrNotAvailable
}

// NewArtworkSource constructs an ArtworkSource from cfg.
func NewArtworkSource(cfg ArtworkSourceConfig) ArtworkSource {
	if newArtworkSourceFunc != nil {
		return newArtworkSourceFunc(cfg)
	}
	return nil
}

// ResizePoster scales a poster JPEG to width.
func ResizePoster(data []byte, width int) ([]byte, error) {
	return artwork.ResizePoster(data, width)
}

// TitlePosterStamp returns the deterministic stamp for a title's generated poster.
func TitlePosterStamp(t *Title) (string, bool) {
	p, ok := artwork.ForTitle(t)
	if !ok {
		return "", false
	}
	return p.Stamp(), true
}

// RenderTitlePoster renders Ether's fallback poster for a title with no artwork.
func RenderTitlePoster(ctx context.Context, generated TitlePosterRenderer, t *Title, width int) ([]byte, string, bool, error) {
	if generated == nil {
		return nil, "", false, nil
	}
	p, ok := artwork.ForTitle(t)
	if !ok {
		return nil, "", false, nil
	}
	p.Width = width
	data, ct, err := generated.Title(ctx, p)
	if err != nil {
		return nil, "", false, err
	}
	return data, ct, true, nil
}
