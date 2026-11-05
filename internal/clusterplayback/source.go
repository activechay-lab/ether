package clusterplayback

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodeauthority"
	"github.com/braedonsaunders/ether/internal/nodesource"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/placement"
	"io"
	"net/http"
	"strconv"
	"time"
)

func viewerString(id int64) string { return strconv.FormatInt(id, 10) }

type ownedSource struct {
	route    Route
	handle   nodeauthority.Handle
	life     core.SourceRetirement
	playID   string
	grant    nodesource.Grant
	retiring bool
}
type sourceRequest struct {
	Prepare *PrepareRequest
	Version int
	Op      string
	Route   Route
	Stable  string
	Expires time.Time
	Old     nodesource.Grant
}
type sourceResponse struct {
	Prepared PreparedSource
	Stable   string
	Grant    nodesource.Grant
	Size     int64
	Retired  bool
}

func (r *Runtime) sourceHTTP(w http.ResponseWriter, q *http.Request) {
	peer, e := r.o.Guard.Request(q)
	if e != nil {
		http.Error(w, "denied", 401)
		return
	}
	if q.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	var m sourceRequest
	d := json.NewDecoder(http.MaxBytesReader(w, q.Body, 64<<10))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil || d.Decode(new(any)) != io.EOF || m.Version != 1 {
		http.Error(w, "request", 400)
		return
	}
	reply, e := r.sourceOperation(q.Context(), peer, m)
	if e != nil {
		http.Error(w, "source unavailable", 409)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reply)
}
func (r *Runtime) sourceOperation(ctx context.Context, peer nodewire.Identity, m sourceRequest) (sourceResponse, error) {
	deny := func() (sourceResponse, error) { return sourceResponse{}, ErrDenied }
	if m.Op == "prepare-retire" {
		return r.retirePreparation(ctx, peer, m.Prepare)
	}
	if m.Op == "prepare" {
		return r.prepare(ctx, peer, m.Prepare)
	}
	rt, e := r.route(ctx, m.Route.Fence.Session)
	if e != nil || rt != m.Route || rt.Source != r.o.Self {
		return deny()
	}
	f := rt.Fence
	controller := peer.Cluster == r.o.Self.Cluster && peer.NodeID == f.Controller.ID && peer.Boot == f.Controller.Boot
	target := peer.Cluster == r.o.Self.Cluster && peer.NodeID == f.Target.NodeID && peer.Boot == f.Target.Boot
	if (m.Op == "bind" || m.Op == "retire" || m.Op == "status") && !controller {
		return deny()
	}
	if (m.Op == "resolve" || m.Op == "renew") && !target {
		return deny()
	}
	if m.Op != "bind" && m.Op != "resolve" && m.Op != "renew" && m.Op != "retire" && m.Op != "status" {
		return deny()
	}
	r.mu.Lock()
	s := r.sources[sourceKey(f)]
	closed := r.closed
	r.mu.Unlock()
	if m.Op == "bind" {
		if r.o.Eligible(ctx, peer, config.RoleServer) != nil || r.o.Eligible(ctx, r.o.Self, config.RoleSource) != nil {
			return deny()
		}
		if closed {
			return deny()
		}
		if s != nil {
			r.mu.Lock()
			retiring := s.retiring
			r.mu.Unlock()
			if s.route != rt || retiring {
				return deny()
			}
			return sourceResponse{Stable: s.handle.Value()}, nil
		}
		life, ok := r.o.Playback.SourceRetirement(rt.PlayID)
		if !ok {
			return deny()
		}
		h, e := r.o.SourceAccess.Capture(ctx, rt.Viewer, rt.PlayID, life, r.o.Playback.InputURL(rt.PlayID), rt.Revision)
		if e != nil {
			return deny()
		}
		handle, e := r.authority.Bind(ctx, nodesource.Scope{Fence: f, Viewer: viewerString(rt.Viewer), Revision: rt.Revision}, h, h.Size())
		if e != nil {
			h.Revoke()
			return deny()
		}
		s = &ownedSource{route: rt, handle: handle, life: life, playID: rt.PlayID}
		r.mu.Lock()
		if r.closed || r.sources[sourceKey(f)] != nil || len(r.sources) >= r.o.Capacity {
			r.mu.Unlock()
			_ = r.authority.Revoke(handle)
			return deny()
		}
		r.sources[sourceKey(f)] = s
		r.mu.Unlock()
		return sourceResponse{Stable: handle.Value()}, nil
	}
	if s == nil && (m.Op == "retire" || m.Op == "status") {
		ps, ok := r.o.Playback.Session(rt.PlayID)
		if !ok || ps == nil || ps.UserID != rt.Viewer || core.SourceRevision(ps) != rt.Revision {
			return deny()
		}
		life, ok := r.o.Playback.SourceRetirement(rt.PlayID)
		if !ok {
			return deny()
		}
		candidate := &ownedSource{route: rt, life: life, playID: rt.PlayID}
		r.mu.Lock()
		if s = r.sources[sourceKey(f)]; s == nil && len(r.sources) < r.o.Capacity {
			r.sources[sourceKey(f)] = candidate
			s = candidate
		}
		r.mu.Unlock()
	}
	if s == nil || s.route != rt {
		return deny()
	}
	if m.Op == "retire" || m.Op == "status" {
		if m.Op == "retire" {
			r.mu.Lock()
			s.retiring = true
			r.mu.Unlock()
			_ = r.authority.Revoke(s.handle)
			if r.o.Playback.RetireSource(s.playID, s.life) != nil {
				return sourceResponse{}, ErrPending
			}
		}
		retired := false
		select {
		case <-s.life.Done():
			retired = s.life.Err() == nil && r.bytes.Pending(nodesource.Scope{Fence: f, Viewer: viewerString(rt.Viewer), Revision: rt.Revision}) == 0
		default:
		}
		return sourceResponse{Retired: retired}, nil
	}
	handle, scope, size, e := r.authority.Resolve(ctx, nodesource.Peer{NodeID: peer.NodeID, Boot: peer.Boot}, m.Stable)
	if e != nil || handle != s.handle {
		return deny()
	}
	var lease nodeauthority.Lease
	if m.Op == "renew" {
		lease, e = r.authority.Renew(ctx, handle, m.Old, m.Expires)
	} else {
		// Resolve retries never mint a second bearer. The first successful grant is
		// immutable; uncertainty is reconciled by retiring this exact generation.
		r.mu.Lock()
		already := s.grant.Credential != ""
		r.mu.Unlock()
		if already {
			return deny()
		}
		lease, e = r.authority.Issue(ctx, handle, m.Expires)
	}
	if e != nil {
		return deny()
	}
	g := lease.Grant()
	if g.Scope != scope {
		return deny()
	}
	r.mu.Lock()
	s.grant = g
	r.mu.Unlock()
	return sourceResponse{Stable: handle.Value(), Grant: g, Size: size}, nil
}
func (r *Runtime) sourceCall(ctx context.Context, rt Route, m sourceRequest) (sourceResponse, error) {
	m.Version = 1
	m.Route = rt
	if rt.Source == r.o.Self {
		return r.sourceOperation(ctx, r.o.Self, m)
	}
	c, ep, e := r.controlClient(ctx, rt.Source)
	if e != nil {
		return sourceResponse{}, e
	}
	defer c.CloseIdleConnections()
	b, e := json.Marshal(m)
	if e != nil {
		return sourceResponse{}, ErrDenied
	}
	q, e := http.NewRequestWithContext(ctx, "POST", ep, bytes.NewReader(b))
	if e != nil {
		return sourceResponse{}, ErrDenied
	}
	q.Header.Set("Content-Type", "application/json")
	resp, e := c.Do(q)
	if e != nil {
		return sourceResponse{}, ErrPending
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return sourceResponse{}, ErrDenied
	}
	var out sourceResponse
	d := json.NewDecoder(io.LimitReader(resp.Body, 64<<10))
	if d.Decode(&out) != nil || d.Decode(new(any)) != io.EOF {
		return sourceResponse{}, ErrPending
	}
	return out, nil
}
func randomToken() (string, error) {
	var b [24]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return hex.EncodeToString(b[:]), nil
}

var _ = placement.ErrFenced

func sourceKey(f placement.Fence) string {
	return f.Session + "/" + strconv.FormatInt(f.Generation, 10)
}
