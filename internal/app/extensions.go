package app

import (
	"context"
	"net/http"

	"github.com/braedonsaunders/ether/internal/simkl"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/cardigann"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/resolver"
)

// extensionHandlers keeps optional owner features behind the same LAN, token
// and CSRF boundaries as the existing admin API.
func (a *App) extensionHandlers() map[string]http.Handler {
	m := map[string]http.Handler{"safety/policies": a.safety.Handler()}
	m["viewer-plex-defaults"] = a.users.PlexDefaultsHandler()
	m["simkl"] = simkl.Handler(simkl.HandlerDeps{Device: a.simklDevice, Service: a.simklService, Enabled: func() bool { c := a.cfg.Get().Simkl; return c.Enabled && c.ClientID != "" }, Viewer: func(ctx context.Context, id int64) bool {
		users, err := a.users.Users(ctx)
		if err != nil {
			return false
		}
		for _, u := range users {
			if u.ID == id {
				return true
			}
		}
		return false
	}})
	m["cache"] = a.cacheHandler()
	m["availability"] = a.availability.Handler()
	m["stats"] = a.stats.Handler()
	m["experience"] = a.stats.ExperienceHandler()
	m["indexer-usage"] = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.indexers == nil {
			http.NotFound(w, r)
			return
		}
		a.indexers.UsageHandler().ServeHTTP(w, r)
	})
	m["notifications"] = a.notifications.Handler()
	m["livetv"] = a.livetv.Handler()
	m["calendar"] = a.calendar.Handler()
	m["safety/diagnostics"] = a.diagnosticsHandler()
	m["playback"] = a.playbackPolicyHandler()
	m["quality"] = a.qualityHandler()
	m["indexer-sites"] = a.sites.Handler(cardigann.HandlerDeps{
		Indexers: func() []config.Indexer { return a.cfg.Get().Indexers },
		Unredact: func(ix config.Indexer) config.Indexer { return admin.UnredactIndexer(ix, a.cfg.Get()) },
	})
	m["stream-preferences"] = a.trackprefs.Handler()
	m["markers"] = a.markers.Handler()
	m["subtitle-sync"] = a.subtitleSyncHandler()
	m["metadata"] = a.episodeorder.Router()
	m["updates"] = a.updatesHandler()
	m["server/restart"] = a.restartHandler()
	m["cluster"] = a.clusterHandler()
	var syncQuality func(ctx context.Context) (resolver.QualitySyncReport, error)
	if a.qualitySync != nil {
		syncQuality = a.qualitySync.Sync
	}
	m["releases"] = a.resolver.ConsoleHandler(resolver.ConsoleDeps{
		Catalog: a.catalog,
		Invalidate: func(key core.RatingKey) {
			if a.playback != nil {
				// Every node drops its warm entries for the title.
				a.playback.InvalidateTitleEverywhere(key)
			}
		},
		Logger:      a.log.With("component", "release-console"),
		Guides:      a.guides,
		Dictionarry: a.dictionarry,
		SaveConfig:  a.saveValidated,
		SyncQuality: syncQuality,
	})
	a.discoveryHandlers(m)
	adoptionHandlers(a, m)
	a.extensibilityHandlers(m)
	return m
}
