package clusterplayback

import (
	"context"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/nodewire/outbound"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
)

const mediaReadHeader = "X-Ether-Media-Read"

func readOperation(op string) bool {
	switch op {
	case "status", "inspect", "playlist", "segment", "rung-playlist", "rung-segment", "manifest", "init", "media", "http", "soft-subtitle":
		return true
	}
	return false
}

// Media replicas observe execution authority only. They never elect, renew,
// reserve, stop or manufacture ownership to read an already admitted encoder.
func (r *Runtime) NeutralMediaCurrent(ctx context.Context, id string) bool {
	route, err := r.route(ctx, id)
	if err != nil || r.deliveryAuthority(ctx, route) != nil {
		return false
	}
	var stopped int
	return r.o.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM clusterplayback_neutral_stops WHERE session=?`, id).Scan(&stopped) == nil && stopped == 0
}

func (r *Runtime) mediaServer(ctx context.Context, id string) (nodewire.Identity, error) {
	route, err := r.route(ctx, id)
	if err != nil {
		return nodewire.Identity{}, err
	}
	if route.Fence.Generation == 0 {
		c, err := r.CurrentCoordinator(ctx, id)
		return c.Identity, err
	}
	// A server role alone does not establish that the calling public handler
	// can restore this session's protocol binding. Select replicas through the
	// configured discovery path; without it, preserve the indexed owner hop.
	if r.o.MediaServers != nil {
		ids, err := r.o.MediaServers(ctx)
		if err != nil {
			return nodewire.Identity{}, err
		}
		for _, peer := range ids {
			if r.o.MemberRole(ctx, peer, config.RoleServer) == nil {
				return peer, nil
			}
		}
	}
	c, err := r.CurrentCoordinator(ctx, id)
	return c.Identity, err
}

// Retry only connection failures before a response exists. Never splice bodies,
// replay control operations or turn a policy denial into a successful response.
type mediaFailoverTransport struct {
	runtime *Runtime
	first   http.RoundTripper
	owner   nodewire.Identity
	path    string
}

func (t *mediaFailoverTransport) RoundTrip(q *http.Request) (*http.Response, error) {
	response, err := t.first.RoundTrip(q)
	if err == nil || t.runtime.o.MediaServers == nil {
		return response, err
	}
	ids, listErr := t.runtime.o.MediaServers(q.Context())
	if listErr != nil {
		return nil, listErr
	}
	for _, peer := range ids {
		if peer == t.owner || t.runtime.o.MemberRole(q.Context(), peer, config.RoleServer) != nil {
			continue
		}
		endpoint, tls, check, e := t.runtime.peer(q.Context(), peer, t.path)
		if e != nil {
			continue
		}
		transport, e := outbound.New(endpoint, tls, 500*time.Millisecond, check)
		if e != nil {
			continue
		}
		transport.ResponseHeaderTimeout(t.runtime.o.EncoderSegmentWait)
		clone := q.Clone(q.Context())
		target, _ := http.NewRequestWithContext(q.Context(), q.Method, endpoint, nil)
		clone.URL = target.URL
		clone.Host = ""
		response, err = transport.RoundTrip(clone)
		transport.CloseIdleConnections()
		if err == nil {
			return response, nil
		}
	}
	return nil, err
}

// A read adapter is independent of the execution map and has no control lease.
// Its exact fence is checked by the worker on every operation and output body.
func (r *Runtime) mediaSession(id string) (core.HLSSession, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if r.o.MemberRole(ctx, r.o.Self, config.RoleServer) != nil {
		return nil, false
	}
	route, err := r.route(ctx, id)
	if err != nil || route.Fence.Generation <= 0 || r.deliveryAuthority(ctx, route) != nil {
		return nil, false
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, false
	}
	if cached := r.mediaSessions[id]; cached != nil && cached.route.Fence == route.Fence {
		r.mu.Unlock()
		return cached.session, true
	}
	r.mu.Unlock()
	endpoint, tls, check, err := r.peer(ctx, nodewire.Identity{Cluster: r.o.Self.Cluster, NodeID: route.Fence.Target.NodeID, Boot: route.Fence.Target.Boot}, WorkerPath)
	if err != nil {
		return nil, false
	}
	adapter, err := remotetranscode.NewAdapter(remotetranscode.AdapterOptions{Endpoint: endpoint, Client: &http.Client{Transport: &http.Transport{TLSClientConfig: tls}}, RecheckServer: check, Capacity: 1, Capabilities: r.o.RemoteCapabilities, Decide: r.o.Decide, RequestHeaders: func(context.Context) (http.Header, error) { return http.Header{mediaReadHeader: []string{"1"}}, nil }, Assign: func(context.Context, core.HLSRequest) (remotetranscode.Assignment, error) {
		return remotetranscode.Assignment{}, ErrDenied
	}})
	if err != nil {
		return nil, false
	}
	session, err := adapter.Attach(ctx, remoteIdentity(route))
	if err != nil {
		adapter.Close()
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || len(r.mediaSessions) >= r.o.HistoryCapacity && r.mediaSessions[id] == nil {
		adapter.Close()
		return nil, false
	}
	if old := r.mediaSessions[id]; old != nil {
		old.adapter.Close()
	}
	r.mediaSessions[id] = &mediaReplica{route: route, adapter: adapter, session: session}
	return session, true
}

type mediaReplica struct {
	route   Route
	adapter *remotetranscode.Adapter
	session *remotetranscode.Session
}
