package clusterplayback

import (
	"context"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/placement"
	"net/http"
	"sync"
	"time"
)

// RemotePlayback is an explicit specialist broker backend. Full-server and Plex
// callers retain their existing local Manager and its optional interfaces.
type RemotePlaybackOptions struct {
	Estimate     func(context.Context, *core.Title, core.ClientCaps) core.MediaInfo
	SelectSource func(context.Context) (nodewire.Identity, error)
	Capacity     int
}
type RemotePlayback struct {
	r       *Runtime
	o       RemotePlaybackOptions
	mu      sync.Mutex
	entries map[string]*remotePreparation
}
type remotePreparation struct {
	operation                    string
	owner                        nodewire.Identity
	request                      PrepareRequest
	prepared                     PreparedSource
	snapshot                     *core.PlaySession
	done                         chan struct{}
	preparing, retiring, retired bool
	pins                         int
}
type remoteSource struct {
	revision, release string
	size              int64
}

func (s remoteSource) SourceRevision() string            { return s.revision }
func (s remoteSource) Size() int64                       { return s.size }
func (s remoteSource) Name() string                      { return "source" }
func (s remoteSource) ReleaseID() string                 { return s.release }
func (s remoteSource) ReadAt([]byte, int64) (int, error) { return 0, core.ErrNotAvailable }
func (s remoteSource) Close() error                      { return core.ErrNotAvailable }

type remoteLifetime struct {
	p *RemotePlayback
	e *remotePreparation
}

func (l remoteLifetime) Done() <-chan struct{} { return l.e.done }
func (l remoteLifetime) Err() error {
	l.p.mu.Lock()
	defer l.p.mu.Unlock()
	if l.e.retired {
		return nil
	}
	return ErrPending
}
func NewRemotePlayback(r *Runtime, o RemotePlaybackOptions) (*RemotePlayback, error) {
	if r == nil || o.Estimate == nil || o.SelectSource == nil || o.Capacity < 1 || o.Capacity > 4096 {
		return nil, ErrDenied
	}
	p := &RemotePlayback{r: r, o: o, entries: map[string]*remotePreparation{}}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.remotePlayback != nil || r.closed {
		return nil, ErrDenied
	}
	r.remotePlayback = p
	return p, nil
}
func (p *RemotePlayback) Estimate(c context.Context, t *core.Title, caps core.ClientCaps) core.MediaInfo {
	return p.o.Estimate(c, t, caps)
}
func (p *RemotePlayback) Prepare(c context.Context, v *core.User, k core.RatingKey, caps core.ClientCaps) (*core.PlaySession, error) {
	s, _, e := p.PrepareSurfaceSource(c, v, k, caps, "", false)
	return s, e
}
func (p *RemotePlayback) PrepareSurface(c context.Context, v *core.User, k core.RatingKey, caps core.ClientCaps, release string, exact bool) (*core.PlaySession, error) {
	s, _, e := p.PrepareSurfaceSource(c, v, k, caps, release, exact)
	return s, e
}
func (p *RemotePlayback) PrepareSurfaceSource(c context.Context, v *core.User, k core.RatingKey, caps core.ClientCaps, release string, exact bool) (*core.PlaySession, core.SourceRetirement, error) {
	if v == nil || v.ID <= 0 || caps.ClientID == "" {
		return nil, nil, core.ErrUnauthorized
	}
	if prior, ok := p.SessionForSurface(string(caps.Protocol), v.ID, caps.ClientID, k, release, exact); ok {
		return prior, p.lifetime(prior.ID), nil
	}
	owner, err := p.o.SelectSource(c)
	if err != nil {
		return nil, nil, err
	}
	if p.r.o.Eligible(c, owner, config.RoleSource) != nil {
		return nil, nil, ErrDenied
	}
	operation, err := randomToken()
	if err != nil {
		return nil, nil, err
	}
	id := "remote-" + operation
	entry := &remotePreparation{operation: operation, owner: owner, request: PrepareRequest{Operation: operation, Viewer: v.ID, Key: k, Caps: caps, ReleaseID: release, Exact: exact, Protocol: "hls"}, preparing: true, done: make(chan struct{})}
	p.mu.Lock()
	if len(p.entries) >= p.o.Capacity {
		p.mu.Unlock()
		return nil, nil, ErrDenied
	}
	p.entries[id] = entry
	p.mu.Unlock()
	life := remoteLifetime{p: p, e: entry}
	prepared, err := p.r.PrepareSource(c, owner, entry.request)
	p.mu.Lock()
	entry.preparing = false
	if err == nil && prepared.Owner == owner && prepared.PlayID != "" && prepared.Revision != "" && (!exact || prepared.ReleaseID == release) {
		entry.prepared = prepared
		entry.snapshot = &core.PlaySession{ID: id, UserID: v.ID, ClientID: caps.ClientID, Key: k, Client: caps, Info: prepared.Info, StartedAt: time.Now(), PreparedOnly: true, Resolution: &core.Resolution{Title: k, Release: core.Release{ID: prepared.ReleaseID}}, Source: remoteSource{revision: prepared.Revision, release: prepared.ReleaseID, size: prepared.Info.Size}}
	} else {
		entry.retiring = true
		if err == nil {
			err = ErrDenied
		}
	}
	snapshot := copyPlaySession(entry.snapshot)
	p.mu.Unlock()
	return snapshot, life, err
}
func copyPlaySession(s *core.PlaySession) *core.PlaySession {
	if s == nil {
		return nil
	}
	b, e := json.Marshal(s.Info)
	if e != nil {
		return nil
	}
	out := *s
	_ = json.Unmarshal(b, &out.Info)
	if s.Resolution != nil {
		resolution := *s.Resolution
		out.Resolution = &resolution
	}
	return &out
}
func (p *RemotePlayback) lifetime(id string) core.SourceRetirement {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.entries[id]; e != nil {
		return remoteLifetime{p: p, e: e}
	}
	return nil
}
func (p *RemotePlayback) SourceRetirement(id string) (core.SourceRetirement, bool) {
	life := p.lifetime(id)
	return life, life != nil
}
func (p *RemotePlayback) Session(id string) (*core.PlaySession, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entries[id]
	if e == nil || e.retired || e.retiring || e.snapshot == nil {
		return nil, false
	}
	return copyPlaySession(e.snapshot), true
}
func (p *RemotePlayback) SessionFor(viewer int64, client string, k core.RatingKey) (*core.PlaySession, bool) {
	return p.SessionForSurface("", viewer, client, k, "", false)
}
func (p *RemotePlayback) SessionForSurface(surface string, viewer int64, client string, k core.RatingKey, release string, exact bool) (*core.PlaySession, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.entries {
		if e.retiring || e.retired || e.snapshot == nil {
			continue
		}
		s := e.snapshot
		if s.UserID == viewer && s.ClientID == client && s.Key == k && (surface == "" || string(s.Client.Protocol) == surface) && (!exact || e.prepared.ReleaseID == release) {
			return copyPlaySession(s), true
		}
	}
	return nil, false
}
func (p *RemotePlayback) Sessions() []*core.PlaySession {
	p.mu.Lock()
	defer p.mu.Unlock()
	var list []*core.PlaySession
	for _, e := range p.entries {
		if !e.retiring && !e.retired && e.snapshot != nil {
			list = append(list, copyPlaySession(e.snapshot))
		}
	}
	return list
}
func (p *RemotePlayback) InputURL(string) string { return "" }
func (p *RemotePlayback) Failover(context.Context, string, time.Duration) (*core.PlaySession, error) {
	return nil, core.ErrNotAvailable
}
func (p *RemotePlayback) Stop(id string) {
	p.mu.Lock()
	if e := p.entries[id]; e != nil {
		e.retiring = true
	}
	p.mu.Unlock()
}
func (p *RemotePlayback) RetireSource(id string, life core.SourceRetirement) error {
	expected, ok := life.(remoteLifetime)
	if !ok || expected.p != p {
		return ErrDenied
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entries[id] != expected.e {
		return ErrDenied
	}
	expected.e.retiring = true
	return nil
}
func (p *RemotePlayback) MarkSurfaceOpened(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entries[id]
	if e == nil || e.snapshot == nil || e.retired || e.retiring {
		return false
	}
	e.snapshot.PreparedOnly = false
	return true
}
func (p *RemotePlayback) MarkSurfacePrepared(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entries[id]
	if e == nil || e.snapshot == nil || e.retired || e.retiring {
		return false
	}
	e.snapshot.PreparedOnly = true
	return true
}
func (p *RemotePlayback) prepared(id string) (PreparedSource, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entries[id]
	if e == nil || e.prepared.PlayID == "" || e.retiring || e.retired {
		return PreparedSource{}, false
	}
	return e.prepared, true
}
func (p *RemotePlayback) CurrentSource(c context.Context, id string) (core.PlaybackSourceObservation, error) {
	p.mu.Lock()
	e := p.entries[id]
	if e == nil || e.retiring || e.retired || e.snapshot == nil {
		p.mu.Unlock()
		return core.PlaybackSourceObservation{}, core.ErrSourceChanged
	}
	owner, operation, expected := e.owner, e.operation, e.prepared
	p.mu.Unlock()
	response, err := p.r.sourceCall(c, Route{Source: owner}, sourceRequest{Op: "prepare-status", Prepare: &PrepareRequest{Operation: operation}})
	if err != nil || response.Prepared.Owner != expected.Owner || response.Prepared.PlayID != expected.PlayID || response.Prepared.Revision != expected.Revision {
		return core.PlaybackSourceObservation{}, core.ErrSourceChanged
	}
	return core.PlaybackSourceObservation{Revision: expected.Revision, ReleaseID: expected.ReleaseID}, nil
}
func (p *RemotePlayback) PinSourceRevision(c context.Context, id, rev string) error {
	observation, err := p.CurrentSource(c, id)
	if err != nil || observation.Revision != rev {
		return core.ErrSourceChanged
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entries[id]
	if e == nil || e.retiring || e.retired {
		return core.ErrSourceChanged
	}
	e.pins++
	return nil
}
func (p *RemotePlayback) UnpinSourceRevision(id string) {
	p.mu.Lock()
	if e := p.entries[id]; e != nil && e.pins > 0 {
		e.pins--
	}
	p.mu.Unlock()
}
func (p *RemotePlayback) tick(c context.Context) error {
	p.mu.Lock()
	list := make([]*remotePreparation, 0, len(p.entries))
	for _, e := range p.entries {
		if e.retiring && !e.retired && !e.preparing {
			list = append(list, e)
		}
	}
	p.mu.Unlock()
	var result error
	for _, e := range list {
		p.mu.Lock()
		needsReconcile := e.prepared.PlayID == ""
		p.mu.Unlock()
		if needsReconcile {
			prepared, err := p.r.PrepareSource(c, e.owner, e.request)
			if err != nil {
				result = ErrPending
				continue
			}
			p.mu.Lock()
			e.prepared = prepared
			p.mu.Unlock()
		}
		retired, err := p.r.RetirePreparedSource(c, e.owner, e.operation)
		if err != nil || !retired {
			result = ErrPending
			continue
		}
		p.mu.Lock()
		if !e.retired {
			e.retired = true
			close(e.done)
		}
		p.mu.Unlock()
	}
	return result
}
func (p *RemotePlayback) ServeRange(w http.ResponseWriter, q *http.Request, id string) {
	p.serveRange(w, q, id)
}

var _ core.Playback = (*RemotePlayback)(nil)
var _ core.SurfaceScopedPlayback = (*RemotePlayback)(nil)
var _ core.OwnedSourcePlayback = (*RemotePlayback)(nil)
var _ core.PlaybackRevisionPin = (*RemotePlayback)(nil)
var _ core.PlaybackSourceSnapshot = (*RemotePlayback)(nil)

// BindClientSession publishes only durable routing facts. No grant is persisted.
func (p *RemotePlayback) BindClientSession(ctx context.Context, session, playID string) error {
	p.mu.Lock()
	e := p.entries[playID]
	if e == nil || e.snapshot == nil || e.retiring || e.retired {
		p.mu.Unlock()
		return ErrDenied
	}
	prepared, viewer := e.prepared, e.request.Viewer
	p.mu.Unlock()
	return p.r.putRoute(ctx, Route{Fence: placement.Fence{Session: session, Controller: placement.Controller{ID: p.r.o.Self.NodeID, Boot: p.r.o.Self.Boot}}, Source: prepared.Owner, Viewer: viewer, PlayID: prepared.PlayID, Revision: prepared.Revision})
}
func (p *RemotePlayback) pending() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, e := range p.entries {
		if !e.retired {
			n++
		}
	}
	return n
}
func (p *RemotePlayback) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.entries {
		e.retiring = true
	}
}
