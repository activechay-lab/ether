package clusterplayback

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/nodewire/outbound"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
	"io"
	"net/http"
	"time"
)

const EvidencePath = "/cluster/playback/terminal-evidence"

var errEvidenceUnsupported = errors.New("clusterplayback: exact terminal evidence unsupported")

type sourceTerminalEvidence struct {
	Version                                                               uint8
	Node                                                                  nodewire.Identity
	Fence                                                                 placement.Fence
	Viewer                                                                int64
	PlayID, Revision                                                      string
	Size                                                                  int64
	AdmissionSealed, CallbacksJoined, BackendNilTerminal, ProducersJoined bool
}

func (s sourceTerminalEvidence) digest() ([32]byte, error) {
	if s.Version != 1 || s.Node.Cluster == "" || s.Node.NodeID == "" || s.Node.Boot == "" || s.Fence.Generation <= 0 || s.Viewer <= 0 || s.PlayID == "" || s.Revision == "" || s.Size <= 0 || !s.AdmissionSealed || !s.CallbacksJoined || !s.BackendNilTerminal || !s.ProducersJoined {
		return [32]byte{}, ErrPending
	}
	raw, e := json.Marshal(s)
	if e != nil || len(raw) > 4096 {
		return [32]byte{}, ErrDenied
	}
	return sha256.Sum256(append([]byte("ether/source-terminal-proof/v1\x00"), raw...)), nil
}

type evidenceRequest struct {
	Fence placement.Fence
	Kind  string
}
type evidenceReply struct {
	Fence  placement.Fence
	Node   nodewire.Identity
	Kind   string
	Worker *remotetranscode.TerminalEvidenceSummary `json:",omitempty"`
	Source *sourceTerminalEvidence                  `json:",omitempty"`
}

func (r *Runtime) evidenceHTTP(w http.ResponseWriter, q *http.Request) {
	peer, e := r.o.Guard.Request(q)
	if e != nil || r.o.MemberRole(q.Context(), peer, config.RoleServer) != nil {
		http.Error(w, "denied", 401)
		return
	}
	if q.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	var request evidenceRequest
	d := json.NewDecoder(http.MaxBytesReader(w, q.Body, 8192))
	d.DisallowUnknownFields()
	if d.Decode(&request) != nil || d.Decode(new(any)) != io.EOF {
		http.Error(w, "request", 400)
		return
	}
	reply, e := r.evidenceOperation(q.Context(), peer, request)
	if e != nil {
		code := 409
		if errors.Is(e, remotetranscode.ErrUnsupported) || errors.Is(e, errEvidenceUnsupported) {
			code = 422
		}
		http.Error(w, "evidence unavailable", code)
		return
	}
	raw, e := json.Marshal(reply)
	if e != nil || len(raw) > 8192 {
		http.Error(w, "evidence unsupported", 422)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}
func (r *Runtime) evidenceOperation(ctx context.Context, peer nodewire.Identity, q evidenceRequest) (evidenceReply, error) {
	if peer.Cluster != r.o.Self.Cluster || r.o.MemberRole(ctx, peer, config.RoleServer) != nil {
		return evidenceReply{}, ErrDenied
	}
	record, e := r.o.Placement.Lookup(ctx, q.Fence.Session)
	if e != nil || record.Fence != q.Fence {
		return evidenceReply{}, ErrDenied
	}
	controller := peer.NodeID == q.Fence.Controller.ID && peer.Boot == q.Fence.Controller.Boot
	if record.Authority && !controller {
		return evidenceReply{}, ErrDenied
	}
	route, e := r.route(ctx, q.Fence.Session)
	if e != nil || route.Fence != q.Fence {
		return evidenceReply{}, ErrDenied
	}
	reply := evidenceReply{Fence: q.Fence, Node: r.o.Self, Kind: q.Kind}
	switch q.Kind {
	case "encoder":
		if r.worker == nil || q.Fence.Target != (placement.Target{NodeID: r.o.Self.NodeID, Boot: r.o.Self.Boot}) {
			return evidenceReply{}, ErrDenied
		}
		summary, e := r.worker.TerminalEvidence(ctx, remoteIdentity(route))
		if e != nil {
			return evidenceReply{}, e
		}
		if _, e = summary.Digest(); e != nil || summary.Identity != remoteIdentity(route) {
			return evidenceReply{}, errEvidenceUnsupported
		}
		reply.Worker = &summary
	case "source":
		if r.authority == nil || route.Source != r.o.Self {
			return evidenceReply{}, ErrDenied
		}
		r.mu.Lock()
		source := r.sources[sourceKey(q.Fence, route.SourceGeneration)]
		if source == nil || source.route != route || !source.sealed || !source.retiring || source.binding || source.issuing || source.operations != 0 || source.life == nil || source.size <= 0 {
			r.mu.Unlock()
			return evidenceReply{}, ErrPending
		}
		life, size := source.life, source.size
		r.mu.Unlock()
		select {
		case <-life.Done():
			if life.Err() != nil {
				return evidenceReply{}, ErrPending
			}
		default:
			return evidenceReply{}, ErrPending
		}
		if r.bytes.Pending(scopeForRoute(route)) != 0 {
			return evidenceReply{}, ErrPending
		}
		if !r.sourceJoined(source) {
			return evidenceReply{}, ErrPending
		}
		proof := sourceTerminalEvidence{Version: 1, Node: r.o.Self, Fence: q.Fence, Viewer: route.Viewer, PlayID: route.PlayID, Revision: route.Revision, Size: size, AdmissionSealed: true, CallbacksJoined: true, BackendNilTerminal: true, ProducersJoined: true}
		if _, e = proof.digest(); e != nil {
			return evidenceReply{}, e
		}
		reply.Source = &proof
	default:
		return evidenceReply{}, ErrDenied
	}
	return reply, nil
}
func (r *Runtime) evidenceCall(ctx context.Context, route Route, kind string) (evidenceReply, error) {
	id := route.Source
	if kind == "encoder" {
		id = nodewire.Identity{Cluster: r.o.Self.Cluster, NodeID: route.Fence.Target.NodeID, Boot: route.Fence.Target.Boot}
	}
	request := evidenceRequest{Fence: route.Fence, Kind: kind}
	if id == r.o.Self {
		return r.evidenceOperation(ctx, r.o.Self, request)
	}
	ep, tc, check, e := r.peer(ctx, id, EvidencePath)
	if e != nil {
		return evidenceReply{}, e
	}
	transport, e := outbound.New(ep, tc, 5*time.Second, check)
	if e != nil {
		return evidenceReply{}, e
	}
	defer transport.CloseIdleConnections()
	raw, _ := json.Marshal(request)
	q, e := http.NewRequestWithContext(ctx, "POST", ep, bytes.NewReader(raw))
	if e != nil {
		return evidenceReply{}, e
	}
	q.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Do(q)
	if e != nil {
		return evidenceReply{}, ErrPending
	}
	defer response.Body.Close()
	if response.StatusCode == 422 {
		return evidenceReply{}, errEvidenceUnsupported
	}
	if response.StatusCode != 200 {
		return evidenceReply{}, ErrPending
	}
	raw, e = io.ReadAll(io.LimitReader(response.Body, 8193))
	if e != nil || len(raw) > 8192 {
		return evidenceReply{}, ErrDenied
	}
	var reply evidenceReply
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&reply) != nil || d.Decode(new(any)) != io.EOF || reply.Fence != route.Fence || reply.Node != id || reply.Kind != kind {
		return evidenceReply{}, ErrDenied
	}
	if kind == "encoder" {
		if reply.Worker == nil || reply.Source != nil || reply.Worker.Identity != remoteIdentity(route) {
			return evidenceReply{}, ErrDenied
		}
		if _, e = reply.Worker.Digest(); e != nil {
			return evidenceReply{}, ErrDenied
		}
	} else {
		if reply.Source == nil || reply.Worker != nil || reply.Source.Node != id || reply.Source.Fence != route.Fence || reply.Source.PlayID != route.PlayID || reply.Source.Revision != route.Revision || reply.Source.Viewer != route.Viewer {
			return evidenceReply{}, ErrDenied
		}
		if _, e = reply.Source.digest(); e != nil {
			return evidenceReply{}, ErrDenied
		}
	}
	return reply, nil
}
