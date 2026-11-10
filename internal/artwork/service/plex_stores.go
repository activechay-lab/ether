package service

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/artwork"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/livetv"
)

// NewPlexArtworkStores returns the frontend.PlexArtworkStores implementation
// wired into plex.Deps by internal/app and tests.
func NewPlexArtworkStores() frontend.PlexArtworkStores {
	return plexArtworkStores{}
}

type plexArtworkStores struct{}

func (plexArtworkStores) NewRemoteCache(budget int64, client *http.Client) frontend.ArtworkImageCache {
	return NewRemoteCache(budget, client)
}

func (plexArtworkStores) NewUntrustedImages(client *http.Client) frontend.UntrustedImageFetcher {
	return artwork.NewImages(artwork.ImagesOptions{HTTPClient: client})
}

func (plexArtworkStores) NewSVGRasterizer() frontend.SVGRasterizer {
	return artwork.NewSVGRasterizer()
}

func (plexArtworkStores) NewPaletteCache(db *sql.DB, submit func(string, func(context.Context)) bool, now func() time.Time, log *slog.Logger, compute func(context.Context, frontend.RatingKey, string) (frontend.ArtworkPalette, error)) frontend.ArtworkPaletteCache {
	return NewPaletteCache(db, submit, now, log, compute)
}

func (plexArtworkStores) ComputeItemPalette(ctx context.Context, cache frontend.ArtworkImageCache, imageURL frontend.ImageURLFunc, key frontend.RatingKey, path string, now time.Time, art frontend.ArtworkCollectionArt) (frontend.ArtworkPalette, error) {
	rc, _ := cache.(*RemoteCache)
	return ComputeItemPalette(ctx, rc, imageURL, key, path, now, art)
}

func (plexArtworkStores) NewSource(cfg frontend.ArtworkSourceConfig) frontend.ArtworkSource {
	rc, _ := cfg.Images.(*RemoteCache)
	pc, _ := cfg.Palettes.(*PaletteCache)
	src := Source{
		Catalog:          cfg.Catalog,
		CollectionDevice: cfg.CollectionDevice,
		Collections:      cfg.Collections,
		Playlists:        cfg.Playlists,
		Visibility:       cfg.Visibility,
		Discovery:        cfg.Discovery,
		Images:           rc,
		ImageURL:         cfg.ImageURL,
		Palettes:         pc,
		Now:              cfg.Now,
		Variants:         cfg.Variants,
		Metadata:         cfg.Metadata,
		VirtualSeason:    cfg.VirtualSeason,
		URLImages:        cfg.URLImages,
		Raster:           cfg.Raster,
	}
	if cfg.LiveTV != nil {
		src.LiveTV = liveTVSourceAdapter{host: cfg.LiveTV}
	}
	if gen, ok := cfg.Generated.(interface {
		Title(context.Context, artwork.TitlePoster) ([]byte, string, error)
	}); ok {
		src.Generated = gen
	}
	return plexArtworkSource{Source: src}
}

func (plexArtworkStores) ResizePoster(data []byte, width int) ([]byte, error) {
	return artwork.ResizePoster(data, width)
}

func (plexArtworkStores) TitlePosterStamp(t *frontend.Title) (string, bool) {
	p, ok := artwork.ForTitle(t)
	if !ok {
		return "", false
	}
	return p.Stamp(), true
}

func (plexArtworkStores) RenderTitlePoster(ctx context.Context, generated any, t *frontend.Title, width int) ([]byte, string, bool, error) {
	gen, ok := generated.(interface {
		Title(context.Context, artwork.TitlePoster) ([]byte, string, error)
	})
	if !ok || gen == nil {
		return nil, "", false, nil
	}
	p, ok := artwork.ForTitle(t)
	if !ok {
		return nil, "", false, nil
	}
	p.Width = width
	data, ct, err := gen.Title(ctx, p)
	if err != nil {
		return nil, "", false, err
	}
	return data, ct, true, nil
}

func (plexArtworkStores) DiagnoseLiveTV(stage string, err error) frontend.LiveTVTuneDiagnosis {
	d := livetv.Diagnose(stage, err)
	return frontend.LiveTVTuneDiagnosis{
		Code:           d.Code,
		Stage:          d.Stage,
		UpstreamStatus: d.UpstreamStatus,
		RetryAfter:     d.RetryAfter,
		Message:        d.Error(),
		LogAttrs:       d.LogAttrs(),
	}
}

func (plexArtworkStores) VirtualDASHError(err error) (string, error, bool) {
	var dashErr *livetv.VirtualDASHError
	if !errors.As(err, &dashErr) {
		return "", nil, false
	}
	return dashErr.Message, dashErr.Err, true
}

func (plexArtworkStores) ShowcaseLogo(c frontend.LiveTVChannel) (string, []byte, bool) {
	return livetv.ShowcaseLogo(livetv.FromFrontendChannel(c))
}

type plexArtworkSource struct {
	Source
}

func (s plexArtworkSource) ChannelImage(viewer *frontend.Viewer, channel frontend.LiveTVChannel) frontend.Image {
	return s.Source.ChannelImage(viewer, livetv.FromFrontendChannel(channel))
}

type liveTVSourceAdapter struct {
	host frontend.LiveTVHost
}

func (a liveTVSourceAdapter) AllowedChannels(ctx context.Context, u *core.User) ([]livetv.Channel, error) {
	cs, err := a.host.AllowedChannels(ctx, u)
	if err != nil {
		return nil, err
	}
	out := make([]livetv.Channel, len(cs))
	for i := range cs {
		out[i] = livetv.FromFrontendChannel(cs[i])
	}
	return out, nil
}

func (a liveTVSourceAdapter) ChannelPermitted(ctx context.Context, u *core.User, id string) (bool, error) {
	return a.host.ChannelPermitted(ctx, u, id)
}

func (a liveTVSourceAdapter) Channel(ctx context.Context, id string) (livetv.Channel, error) {
	c, err := a.host.Channel(ctx, id)
	if err != nil {
		return livetv.Channel{}, err
	}
	return livetv.FromFrontendChannel(c), nil
}
