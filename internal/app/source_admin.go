package app

import (
	"context"
	"crypto/rand"
	"net/http"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend/kit/authhdr"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/sourceadmin"
	"github.com/braedonsaunders/ether/internal/usenet"
)

const localCacheResource = "local-cache"

type sourceOwnerCredential struct{ value string }

func (sourceOwnerCredential) String() string   { return "[redacted owner credential]" }
func (sourceOwnerCredential) GoString() string { return "[redacted owner credential]" }

type sourceOwnerKey struct{}

// Preserve the original credential only in a private typed context. Remote
// nodes verify it independently; a local owner boolean is never transported.
func sourceOwnerContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		credential := strings.TrimSpace(r.Header.Get("X-Plex-Token"))
		if credential == "" {
			credential = strings.TrimSpace(r.Header.Get("X-Ether-Admin-Token"))
		}
		if credential == "" {
			if bearer, ok := authhdr.Bearer(r.Header.Get("Authorization")); ok {
				credential = strings.TrimSpace(bearer)
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sourceOwnerKey{}, sourceOwnerCredential{credential})))
	})
}
func (a *App) verifySourceOwner(ctx context.Context, credential string) error {
	if a.users == nil || credential == "" {
		return sourceadmin.ErrDenied
	}
	grants, err := identity.NewGrants(a.db, a.users, 30*24*time.Hour, nil)
	if err != nil {
		return sourceadmin.ErrDenied
	}
	user, err := grants.Resolve(ctx, "owner", credential)
	if err != nil {
		if core.IsGrantCredential(credential) {
			return sourceadmin.ErrDenied
		}
		if auth.IsOwnerSession(credential) {
			user, err = a.users.OwnerSession(ctx, credential)
		} else {
			user, err = a.users.DurableOwnerToken(ctx, credential)
		}
	}
	if err != nil || user == nil || !user.IsOwner || user.Delegated {
		return sourceadmin.ErrDenied
	}
	current, err := a.users.PolicyUser(ctx, user.ID)
	if err != nil || current == nil || !current.IsOwner || current.Delegated {
		return sourceadmin.ErrDenied
	}
	return nil
}

func (a *App) buildSourceAdmin() error {
	p := a.deployment
	if p == nil || !p.settings.Roles.Has(config.RoleSource) {
		return nil
	}
	self := a.cluster.Self()
	local := nodewire.Identity{Cluster: a.cfg.Get().Server.MachineIdentifier, NodeID: self.ID, Boot: self.Incarnation}
	handler, err := sourceadmin.NewHandler(sourceadmin.Options{Guard: p.guard, Local: local, Timeout: 20 * time.Second, VerifyOwner: a.verifySourceOwner, Authorize: func(ctx context.Context, caller, owner nodewire.Identity, q sourceadmin.Request) error {
		if owner != local || a.privateMemberRole(ctx, caller, config.RoleServer) != nil || a.privateMemberRole(ctx, owner, config.RoleSource) != nil {
			return sourceadmin.ErrDenied
		}
		if q.Operation == sourceadmin.TestProvider || q.Operation == sourceadmin.TestIndexer || q.Operation == sourceadmin.TestDebrid {
			if a.privateEligible(ctx, caller, config.RoleServer) != nil || a.privateEligible(ctx, owner, config.RoleSource) != nil {
				return sourceadmin.ErrDenied
			}
		}
		return a.authorizeSourceResource(q)
	}, Services: a.sourceAdminServices()})
	if err != nil {
		return err
	}
	p.sourceAdmin = handler
	return nil
}
func (a *App) authorizeSourceResource(q sourceadmin.Request) error {
	cfg := a.cfg.Get()
	switch q.Operation {
	case sourceadmin.TestProvider:
		for _, v := range cfg.Usenet.Providers {
			if v.ID != "" && v.ID == q.ResourceID {
				return nil
			}
		}
	case sourceadmin.TestIndexer:
		for _, v := range cfg.Indexers {
			if v.ID != "" && v.ID == q.ResourceID {
				return nil
			}
		}
	case sourceadmin.TestDebrid:
		for _, v := range cfg.Debrid.Providers {
			if v.ID != "" && v.ID == q.ResourceID {
				return nil
			}
		}
	case sourceadmin.CacheStatus:
		if q.ResourceID == localCacheResource && a.cache != nil {
			return nil
		}
	case sourceadmin.Snapshot, sourceadmin.Traces, sourceadmin.ReleaseHistory, sourceadmin.Variants:
		return nil
	}
	return sourceadmin.ErrDenied
}
func sourceState(state string) sourceadmin.Status {
	switch state {
	case "ok":
		return sourceadmin.Healthy
	case "backoff":
		return sourceadmin.Backoff
	case "auth_failed", "failed":
		return sourceadmin.Failed
	default:
		return sourceadmin.Unknown
	}
}
func (a *App) sourceAdminServices() sourceadmin.Services {
	s := sourceadmin.Services{}
	if a.fetcher != nil && a.indexers != nil && a.cache != nil && a.engine != nil {
		s.Snapshot = func(ctx context.Context, limit int) (sourceadmin.Diagnostics, error) {
			d := sourceadmin.Diagnostics{}
			cfg := a.cfg.Get()
			for _, p := range a.fetcher.Stats() {
				for slot, v := range cfg.Usenet.Providers {
					if p.ID == v.ID && len(d.Providers) < limit {
						d.Providers = append(d.Providers, sourceadmin.Provider{Slot: slot, State: sourceState(p.State), MaxConnections: p.MaxConnections, LiveConnections: p.LiveConnections, ActiveConnections: p.ActiveConnections, NodeShare: p.NodeShare})
						break
					}
				}
			}
			remaining, err := a.indexers.Remaining(ctx)
			if err != nil {
				return d, err
			}
			for _, ix := range remaining {
				for slot, v := range cfg.Indexers {
					if (ix.Indexer == v.ID || v.ID == "" && ix.Indexer == v.Name) && len(d.Indexers) < limit {
						d.Indexers = append(d.Indexers, sourceadmin.Indexer{Slot: slot, HitsUsed: ix.HitsUsed, HitsRemaining: ix.HitsRemaining, GrabsUsed: ix.GrabsUsed, GrabsRemaining: ix.GrabsRemaining, RetryAt: ix.RetryAt, Enabled: ix.Enabled, Torrent: ix.Torrent})
						break
					}
				}
			}
			used, budget := a.cache.Usage()
			stats := a.engine.Stats()
			d.Cache = &sourceadmin.Cache{Used: used, Budget: budget, Hits: stats.CacheHits, HotBytes: stats.HotBytes}
			d.Stream = &sourceadmin.Stream{Fetches: stats.Fetches, FetchedBytes: stats.FetchedBytes, Missing: stats.Missing, ActiveSources: stats.ActiveSources, OpenReleases: stats.OpenReleases}
			if a.resolver != nil {
				for _, v := range a.resolver.SourceStartups() {
					var protocol sourceadmin.Protocol
					switch v.Protocol {
					case core.ProtocolUsenet:
						protocol = sourceadmin.Usenet
					case core.ProtocolTorrent:
						protocol = sourceadmin.Torrent
					default:
						continue
					}
					d.Sources = append(d.Sources, sourceadmin.SourceStartup{Protocol: protocol, AverageMillis: v.Average.Milliseconds(), LastMillis: v.Last.Milliseconds(), Samples: v.Samples})
				}
			}
			return d, nil
		}
	}
	if a.fetcher != nil {
		s.TestProvider = func(ctx context.Context, id string) error {
			for _, v := range a.cfg.Get().Usenet.Providers {
				if id != "" && v.ID == id {
					return usenet.TestProvider(ctx, v)
				}
			}
			return sourceadmin.ErrDenied
		}
	}
	if a.indexers != nil {
		s.TestIndexer = func(ctx context.Context, id string) error {
			for _, v := range a.cfg.Get().Indexers {
				if id != "" && v.ID == id {
					if v.IsStremio() {
						return a.addons.Test(ctx, v)
					}
					if v.IsBuiltin() {
						if result := a.sites.Test(ctx, v, false); !result.OK {
							return sourceadmin.ErrUnavailable
						}
						return nil
					}
					return a.indexers.TestIndexer(ctx, v, a.cfg.Get().ClientIdentity)
				}
			}
			return sourceadmin.ErrDenied
		}
	}
	if a.debrid != nil {
		s.TestDebrid = func(ctx context.Context, id string) error {
			for _, v := range a.cfg.Get().Debrid.Providers {
				if id != "" && v.ID == id {
					return a.debrid.Test(ctx, v)
				}
			}
			return sourceadmin.ErrDenied
		}
	}
	if a.cache != nil {
		s.CacheStatus = func(_ context.Context, id string) (sourceadmin.Cache, error) {
			if id != localCacheResource {
				return sourceadmin.Cache{}, sourceadmin.ErrDenied
			}
			used, budget := a.cache.Usage()
			return sourceadmin.Cache{Used: used, Budget: budget}, nil
		}
	}
	if a.resolver != nil {
		s.Traces = func(_ context.Context, q sourceadmin.Query) ([]sourceadmin.Trace, error) {
			rows := a.resolver.Traces()
			out := []sourceadmin.Trace{}
			for _, v := range rows {
				if len(out) >= q.Limit {
					break
				}
				state := sourceadmin.Healthy
				if v.Err != "" {
					state = sourceadmin.Failed
				}
				out = append(out, sourceadmin.Trace{TitleID: int64(v.TitleKey), AtUnixMilli: v.At.UnixMilli(), State: state, Candidates: len(v.Candidates), SearchMillis: v.SearchDuration.Milliseconds(), PreparationMillis: v.SourcePreparationDuration.Milliseconds(), TotalMillis: v.TotalDuration.Milliseconds(), Failover: v.Failover, Pinned: v.Pinned != ""})
			}
			return out, nil
		}
		s.ReleaseHistory = func(ctx context.Context, q sourceadmin.Query) ([]sourceadmin.Release, error) {
			rows, _, err := a.resolver.History(ctx, resolver.HistoryFilter{TitleKey: core.RatingKey(q.TitleID), Limit: q.Limit})
			out := []sourceadmin.Release{}
			for _, v := range rows {
				state := sourceadmin.Healthy
				if v.Error != "" {
					state = sourceadmin.Failed
				}
				out = append(out, sourceadmin.Release{HistoryID: v.ID, TitleID: int64(v.TitleKey), AtUnixMilli: v.At.UnixMilli(), Size: v.Size, State: state, Pinned: v.Pinned, Automatic: v.Chooser != "viewer"})
			}
			return out, err
		}
		s.Variants = func(ctx context.Context, q sourceadmin.Query) ([]sourceadmin.Release, error) {
			title, err := a.catalog.CachedGet(ctx, core.RatingKey(q.TitleID))
			if err != nil {
				return nil, err
			}
			owner, err := a.users.Owner(ctx)
			if err != nil {
				return nil, err
			}
			rows, err := a.resolver.Variants(ctx, core.ResolveRequest{Title: title, User: owner}, q.Limit)
			out := []sourceadmin.Release{}
			for _, v := range rows {
				height := 0
				if video := v.Info.Video(); video != nil {
					height = video.Height
				}
				out = append(out, sourceadmin.Release{TitleID: q.TitleID, Size: v.Release.Size, Height: height, State: sourceadmin.Unknown, Automatic: v.Auto})
			}
			return out, err
		}
	}
	return s
}

func (a *App) callSourceAdmin(ctx context.Context, q sourceadmin.Request) (sourceadmin.Response, error) {
	p := a.deployment
	if p == nil {
		return sourceadmin.Response{}, sourceadmin.ErrUnsupported
	}
	credential, _ := ctx.Value(sourceOwnerKey{}).(sourceOwnerCredential)
	if credential.value == "" {
		return sourceadmin.Response{}, sourceadmin.ErrDenied
	}
	if err := a.verifySourceOwner(ctx, credential.value); err != nil {
		return sourceadmin.Response{}, err
	}
	var peer nodewire.Identity
	nodes, err := a.cluster.Nodes(ctx)
	if err == nil {
		for _, n := range nodes {
			id := nodewire.Identity{Cluster: a.cfg.Get().Server.MachineIdentifier, NodeID: n.ID, Boot: n.Incarnation}
			if a.privateMemberRole(ctx, id, config.RoleSource) == nil {
				peer = id
				break
			}
		}
		if peer.NodeID == "" {
			err = sourceadmin.ErrUnavailable
		}
	}
	if err != nil {
		return sourceadmin.Response{}, err
	}
	endpoint, err := a.privateEndpoint(ctx, peer)
	if err != nil {
		return sourceadmin.Response{}, err
	}
	client, err := sourceadmin.NewClient(sourceadmin.ClientOptions{Guard: p.guard, Peer: peer, Endpoint: endpoint, Timeout: 20 * time.Second, Authorize: func(ctx context.Context, id nodewire.Identity, _ sourceadmin.Request) error {
		self := a.cluster.Self()
		if a.privateMemberRole(ctx, nodewire.Identity{Cluster: peer.Cluster, NodeID: self.ID, Boot: self.Incarnation}, config.RoleServer) != nil {
			return sourceadmin.ErrDenied
		}
		return a.privateMemberRole(ctx, id, config.RoleSource)
	}})
	if err != nil {
		return sourceadmin.Response{}, err
	}
	defer client.CloseIdleConnections()
	q.RequestID = rand.Text()
	return client.Call(ctx, credential.value, q)
}
