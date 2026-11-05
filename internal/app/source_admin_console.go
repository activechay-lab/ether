package app

import (
	"context"
	"reflect"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/newznab"
	"github.com/braedonsaunders/ether/internal/sourceadmin"
	"github.com/braedonsaunders/ether/internal/usenet"
)

// These are real private RPC dependencies, installed only when this server has
// no local source. The original console/CSRF/owner middleware remains in place.
func (a *App) remoteSourceAdminDeps(d *admin.Deps) {
	if a.deployment == nil || a.deployment.settings.Roles.Has(config.RoleSource) {
		return
	}
	snapshot := func(ctx context.Context) (*sourceadmin.Diagnostics, error) {
		response, err := a.callSourceAdmin(ctx, sourceadmin.Request{Operation: sourceadmin.Snapshot, Payload: &sourceadmin.Query{Limit: sourceadmin.MaxItems}})
		return response.Diagnostics, err
	}
	failure := func(err error) any { return map[string]string{"error": "source administration unavailable"} }
	d.ProvidersStatus = func(ctx context.Context) any {
		response, err := snapshot(ctx)
		if err != nil || response == nil {
			return failure(err)
		}
		cfg := a.cfg.Get()
		out := []usenet.ProviderStats{}
		for _, p := range response.Providers {
			if p.Slot < 0 || p.Slot >= len(cfg.Usenet.Providers) {
				continue
			}
			v := cfg.Usenet.Providers[p.Slot]
			state := "unknown"
			switch p.State {
			case sourceadmin.Healthy:
				state = usenet.StateOK
			case sourceadmin.Backoff:
				state = usenet.StateBackoff
			case sourceadmin.Failed:
				state = usenet.StateAuthFail
			}
			out = append(out, usenet.ProviderStats{ID: v.ID, Name: v.Name, Host: v.Host, Priority: v.Priority, Backfill: v.Backfill, State: state, MaxConnections: p.MaxConnections, LiveConnections: p.LiveConnections, ActiveConnections: p.ActiveConnections, NodeShare: p.NodeShare})
		}
		return out
	}
	d.IndexersStatus = func(ctx context.Context) any {
		response, err := snapshot(ctx)
		if err != nil || response == nil {
			return failure(err)
		}
		cfg := a.cfg.Get()
		out := []newznab.Remaining{}
		for _, ix := range response.Indexers {
			if ix.Slot < 0 || ix.Slot >= len(cfg.Indexers) {
				continue
			}
			v := cfg.Indexers[ix.Slot]
			out = append(out, newznab.Remaining{Indexer: v.ID, HitsUsed: ix.HitsUsed, HitsRemaining: ix.HitsRemaining, GrabsUsed: ix.GrabsUsed, GrabsRemaining: ix.GrabsRemaining, RetryAt: ix.RetryAt, Enabled: ix.Enabled, Torrent: ix.Torrent})
		}
		return out
	}
	d.CacheStatus = func(ctx context.Context) any {
		response, err := a.callSourceAdmin(ctx, sourceadmin.Request{Operation: sourceadmin.CacheStatus, ResourceID: localCacheResource})
		if err != nil || response.Cache == nil {
			return failure(err)
		}
		return map[string]any{"used": response.Cache.Used, "budget": response.Cache.Budget, "mode": a.cfg.Get().Cache.Mode}
	}
	d.TestUsenet = func(ctx context.Context, input config.UsenetProvider) error {
		for _, v := range a.cfg.Get().Usenet.Providers {
			if input.ID != "" && input.ID == v.ID && reflect.DeepEqual(input, v) {
				_, err := a.callSourceAdmin(ctx, sourceadmin.Request{Operation: sourceadmin.TestProvider, ResourceID: v.ID})
				return err
			}
		}
		return sourceadmin.ErrDenied
	}
	d.TestIndexer = func(ctx context.Context, input config.Indexer) error {
		for _, v := range a.cfg.Get().Indexers {
			if input.ID != "" && input.ID == v.ID && reflect.DeepEqual(input, v) {
				_, err := a.callSourceAdmin(ctx, sourceadmin.Request{Operation: sourceadmin.TestIndexer, ResourceID: v.ID})
				return err
			}
		}
		return sourceadmin.ErrDenied
	}
	d.TestDebrid = func(ctx context.Context, input config.DebridProvider) error {
		for _, v := range a.cfg.Get().Debrid.Providers {
			if input.ID != "" && input.ID == v.ID && reflect.DeepEqual(input, v) {
				_, err := a.callSourceAdmin(ctx, sourceadmin.Request{Operation: sourceadmin.TestDebrid, ResourceID: v.ID})
				return err
			}
		}
		return sourceadmin.ErrDenied
	}
	d.ResolverTraces = func(ctx context.Context) (any, error) {
		response, err := a.callSourceAdmin(ctx, sourceadmin.Request{Operation: sourceadmin.Traces, Payload: &sourceadmin.Query{Limit: sourceadmin.MaxItems}})
		return response.Traces, err
	}
}
