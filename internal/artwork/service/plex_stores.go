package service

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/livetv"
)

func init() {
	frontend.RegisterArtworkStores(
		func(budget int64, client *http.Client) frontend.ArtworkImageCache {
			return NewRemoteCache(budget, client)
		},
		func(db *sql.DB, submit func(string, func(context.Context)) bool, now func() time.Time, log *slog.Logger, compute func(context.Context, frontend.RatingKey, string) (frontend.ArtworkPalette, error)) frontend.ArtworkPaletteCache {
			return NewPaletteCache(db, submit, now, log, compute)
		},
		func(ctx context.Context, cache frontend.ArtworkImageCache, imageURL frontend.ImageURLFunc, key frontend.RatingKey, path string, now time.Time, art frontend.ArtworkCollectionArt) (frontend.ArtworkPalette, error) {
			rc, _ := cache.(*RemoteCache)
			return ComputeItemPalette(ctx, rc, imageURL, key, path, now, art)
		},
		newFrontendSource,
	)
}

func newFrontendSource(cfg frontend.ArtworkSourceConfig) frontend.ArtworkSource {
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
		Generated:        cfg.Generated,
	}
	if cfg.LiveTV != nil {
		src.LiveTV = liveTVSourceAdapter{host: cfg.LiveTV}
	}
	return frontendArtworkSource{Source: src}
}

type frontendArtworkSource struct {
	Source
}

func (s frontendArtworkSource) ChannelImage(viewer *frontend.Viewer, channel frontend.LiveTVChannel) frontend.Image {
	return s.Source.ChannelImage(viewer, livetv.FromFrontendChannel(channel))
}

// RemoteCache returns the underlying remote artwork cache for frontend.Artwork.
func (a Source) RemoteCache() frontend.ArtworkImageCache {
	if a.Images == nil {
		return nil
	}
	return a.Images
}

// PaletteCache returns the underlying title palette cache for frontend.Artwork.
func (a Source) PaletteCache() frontend.ArtworkPaletteCache {
	if a.Palettes == nil {
		return nil
	}
	return a.Palettes
}

// PosterRenderer returns the fallback title poster renderer for frontend.Artwork.
func (a Source) PosterRenderer() frontend.TitlePosterRenderer {
	return a.Generated
}

// ImageURLFunc returns the TMDB image URL builder for frontend.Artwork.
func (a Source) ImageURLFunc() frontend.ImageURLFunc {
	return a.ImageURL
}

type liveTVSourceAdapter struct {
	host frontend.LiveTV
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
