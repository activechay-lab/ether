package clusterplayback

import (
	"context"
	"crypto/tls"
	"database/sql"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodeauthority"

	"github.com/braedonsaunders/ether/internal/nodesource"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/nodewire/outbound"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const SourcePath = "/cluster/playback/source"
const WorkerPath = "/cluster/playback/worker"

type Options struct {
	MemberRole func(context.Context, nodewire.Identity, config.NodeRole) error
	// Eligible checks live exact boot membership, role, protocol and drain policy.
	Eligible        func(context.Context, nodewire.Identity, config.NodeRole) error
	DB              *sql.DB
	Guard           *nodewire.Guard
	Self            nodewire.Identity
	Roles           config.NodeRoles
	Placement       *placement.Service
	Endpoint        func(context.Context, nodewire.Identity) (string, error)
	ActiveViewer    func(context.Context, int64) (*core.Viewer, error)
	Playback        *playback.Manager
	SourceAccess    *playback.SourceAccess
	Encoder         core.OwnedTranscoder
	Decide          func(core.MediaInfo, core.ClientCaps, core.DecideOptions) core.Decision
	Hardware        core.HWAccel
	Capacity        int
	ClockSkew       time.Duration
	ClockBoundKnown bool
}
type Runtime struct {
	o          Options
	mu         sync.Mutex
	closed     bool
	mux        *http.ServeMux
	authority  *nodeauthority.Authority
	bytes      *nodesource.Handler
	sources    map[string]*ownedSource
	worker     *remotetranscode.Worker
	inputs     map[remotetranscode.Identity]*workerInput
	executions map[string]*execution
}

func NewRuntime(o Options) (*Runtime, error) {
	if o.DB == nil || o.Guard == nil || o.Placement == nil || o.Endpoint == nil || o.Eligible == nil || o.MemberRole == nil || o.Self.Cluster == "" || o.Self.NodeID == "" || o.Self.Boot == "" || !o.ClockBoundKnown || o.ClockSkew < 0 || o.ClockSkew > time.Minute || o.Capacity < 1 || o.Capacity > 4096 {
		return nil, ErrDenied
	}
	r := &Runtime{o: o, mux: http.NewServeMux(), sources: map[string]*ownedSource{}, inputs: map[remotetranscode.Identity]*workerInput{}, executions: map[string]*execution{}}
	if o.Playback != nil {
		if o.SourceAccess == nil || o.ActiveViewer == nil {
			return nil, ErrDenied
		}
		a, e := nodeauthority.New(nodeauthority.Options{Lookup: o.Placement, Policy: r.sourcePolicy, Owner: placement.Target{NodeID: o.Self.NodeID, Boot: o.Self.Boot}, Now: time.Now, ClockBoundKnown: true, ClockSkew: o.ClockSkew, MaxLifetime: 10 * time.Minute, MaxBindings: o.Capacity, MaxGrants: min(4096, o.Capacity*4), MaxGrantsPerBinding: 4})
		if e != nil {
			return nil, e
		}
		r.authority = a
		h, e := nodesource.NewHandler(nodesource.Options{Authority: a, Peer: o.Guard.SourcePeer, Acquire: a.Acquire, Now: time.Now, MaxRequests: o.Capacity, MaxLifetime: 10 * time.Minute, Revalidate: time.Second})
		if e != nil {
			return nil, e
		}
		r.bytes = h
		r.mux.Handle(nodesource.Path, h)
		r.mux.HandleFunc(SourcePath, r.sourceHTTP)
	}
	if o.Encoder != nil {
		w, e := remotetranscode.NewWorker(remotetranscode.WorkerOptions{Boot: o.Self.Boot, Capacity: o.Capacity, MaxLease: 10 * time.Minute, Encoder: o.Encoder, Security: remotetranscode.Security{Authenticate: o.Guard.RemoteAuthenticate, Authorize: r.authorizeWorker}, Resolve: r.resolveInput, ValidateRenewal: r.renewInput})
		if e != nil {
			return nil, e
		}
		r.worker = w
		r.mux.Handle(WorkerPath, w)
	}
	return r, nil
}
func (r *Runtime) Handler() http.Handler { return r.mux }
func (r *Runtime) Ready() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.closed && (r.worker != nil || r.authority != nil)
}
func (r *Runtime) peer(ctx context.Context, id nodewire.Identity, path string) (string, *tls.Config, func(context.Context, tls.ConnectionState) error, error) {
	origin, e := r.o.Endpoint(ctx, id)
	if e != nil {
		return "", nil, nil, ErrDenied
	}
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", nil, nil, ErrDenied
	}
	tc, e := r.o.Guard.ClientTLS(id, u.Hostname())
	if e != nil {
		return "", nil, nil, e
	}
	check := func(c context.Context, s tls.ConnectionState) error {
		return r.o.Guard.RecheckServer(c, s, id, u.Hostname())
	}
	return "https://" + u.Host + path, tc, check, nil
}
func (r *Runtime) controlClient(ctx context.Context, id nodewire.Identity) (*http.Client, string, error) {
	ep, tc, check, e := r.peer(ctx, id, SourcePath)
	if e != nil {
		return nil, "", e
	}
	t, e := outbound.New(ep, tc, 5*time.Second, check)
	if e != nil {
		return nil, "", e
	}
	return &http.Client{Transport: t, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, ep, nil
}

// Cleanup may block in backend Close. Never run it on the lease/control executor.
func (r *Runtime) Cleanup() {
	if r.bytes != nil {
		r.bytes.Reconcile()
	}
	if r.o.SourceAccess != nil {
		r.o.SourceAccess.Reconcile()
	}
	r.mu.Lock()
	list := make([]*workerInput, 0, len(r.inputs))
	for _, v := range r.inputs {
		list = append(list, v)
	}
	r.mu.Unlock()
	for _, v := range list {
		v.gateway.Reconcile()
	}
}
func (r *Runtime) Close() error {
	r.mu.Lock()
	r.closed = true
	list := make([]*execution, 0, len(r.executions))
	for _, x := range r.executions {
		list = append(list, x)
	}
	inputs := make([]*workerInput, 0, len(r.inputs))
	for _, x := range r.inputs {
		inputs = append(inputs, x)
	}
	r.mu.Unlock()
	if r.worker != nil {
		_ = r.worker.Close()
	}
	for _, x := range list {
		r.Stop(x.route.Fence.Session)
	}
	for _, x := range inputs {
		x.cancel()
		_ = x.gateway.Close()
		<-x.done
		x.client.Close()
	}
	return nil
}
func (r *Runtime) sourcePolicy(ctx context.Context, owner placement.Target, s nodesource.Scope) error {
	route, e := r.route(ctx, s.Fence.Session)
	if e != nil || route.Fence != s.Fence || route.Source.NodeID != owner.NodeID || route.Source.Boot != owner.Boot || route.Revision != s.Revision || viewerString(route.Viewer) != s.Viewer {
		return ErrDenied
	}
	for _, pair := range []struct {
		id   nodewire.Identity
		role config.NodeRole
	}{
		{route.Source, config.RoleSource},
		{nodewire.Identity{Cluster: r.o.Self.Cluster, NodeID: s.Fence.Controller.ID, Boot: s.Fence.Controller.Boot}, config.RoleServer},
		{nodewire.Identity{Cluster: r.o.Self.Cluster, NodeID: s.Fence.Target.NodeID, Boot: s.Fence.Target.Boot}, config.RoleTranscode},
	} {
		if r.o.MemberRole(ctx, pair.id, pair.role) != nil {
			return ErrDenied
		}
	}
	v, e := r.o.ActiveViewer(ctx, route.Viewer)
	if e != nil || v == nil || v.ID != route.Viewer {
		return ErrDenied
	}
	return nil
}
