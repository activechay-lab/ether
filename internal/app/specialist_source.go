package app

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/safety"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// buildSourceRole constructs only catalogue/viewer policy readers, provider
// transport/resolution, and local source ownership. It has no registrar, public
// frontend, personalization workers, encoding or leader singleton jobs.
func (a *App) buildSourceRole(ctx context.Context, cfg config.Config) error {
	var err error
	a.safety, err = safety.New(ctx, a.db)
	if err != nil {
		return err
	}
	if err = a.buildCatalogue(ctx, cfg); err != nil {
		return err
	}
	a.safety.SetCatalogPolicy(a.catalog.Visible)
	a.users, err = auth.New(ctx, a.db, auth.Options{Logger: a.log})
	if err != nil {
		return err
	}
	a.safety.SetPlexShare(func(u *core.User) safety.PlexShare {
		if u == nil {
			return safety.PlexShare{}
		}
		d, ok := a.users.PlexDefaults(u.ID)
		if !ok {
			return safety.PlexShare{}
		}
		return safety.PlexShare{FilterMovies: d.FilterMovies, FilterTelevision: d.FilterTelevision, AllowSync: d.AllowSync}
	})
	bus := a.clusterNode()
	a.users.SetBus(bus)
	a.safety.SetBus(bus)
	a.closers = append(a.closers, func() { a.users.Close(); a.safety.Close() })
	cfg.Cache = cfg.EffectiveCache()
	if err = a.buildSources(ctx, cfg); err != nil {
		return err
	}
	a.resolver.SetBus(bus)
	var tc core.Transcoder = directOnly{}
	if active := a.activeTranscoder(); active != nil {
		tc = active
	}
	a.playback, err = playback.New(playback.Deps{
		Resolver: a.resolver, Engine: playbackEngine(a), Transcoder: tc, Catalog: a.catalog, Cache: a.cache, DB: a.db, Logger: a.log, Cluster: a.clusterNode(), Availability: a.dbAvail,
		NativeProbe: func(ctx context.Context, src core.MediaSource) (*core.MediaInfo, error) {
			return transcode.ProbeSource(ctx, src, src.Size())
		},
		NativeTrailer: func(ctx context.Context, src core.MediaSource, info *core.MediaInfo) bool {
			return transcode.ProbeTrailer(ctx, src, src.Size(), info)
		},
		FetchNZB: playbackFetchNZB(a, a.opts.DataDir),
		SelectProfile: func(caps core.ClientCaps, u *core.User) core.QualityProfile {
			return resolver.SelectProfile(a.cfg.Read().Quality, caps, u)
		},
	}, cfg, playback.Options{DataDir: a.opts.DataDir, PayloadReplayGrace: 5 * time.Second})
	if err != nil {
		return err
	}
	a.closers = append(a.closers, func() { _ = a.playback.Close() })
	a.cache.Protect(a.protectedReleases)
	return nil
}
