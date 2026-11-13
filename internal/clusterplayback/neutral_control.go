package clusterplayback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/nodewire/outbound"
)

const NeutralControlPath = "/cluster/playback/neutral-control"

type neutralControlRequest struct {
	ID, Operation string
	ActorID       int64
	Protocol      frontend.Protocol
	Device        core.Device
	Owner         bool
	Epoch         int64
	Timeline      frontend.Timeline
	Stream        frontend.StreamRequest
}
type neutralControlReply struct{ Path, Error string }
type neutralControlHop struct{}

func (r *Runtime) BindNeutralControl(control func(context.Context, frontend.SessionAccess, string, string, frontend.Timeline, frontend.StreamRequest) (string, error)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if control == nil || r.neutralControl != nil || r.closed {
		return ErrDenied
	}
	r.neutralControl = control
	return nil
}

func (r *Runtime) ForwardNeutralControl(ctx context.Context, a frontend.SessionAccess, id, operation string, timeline frontend.Timeline, stream frontend.StreamRequest) (string, bool, error) {
	if ctx.Value(neutralControlHop{}) != nil {
		return r.forwardNeutralControlOnce(ctx, a, id, operation, timeline, stream)
	}
	ctx, cancel := context.WithTimeout(ctx, coordinationLease+3*r.o.ClockSkew+4*time.Second)
	defer cancel()
	for {
		path, handled, err := r.forwardNeutralControlOnce(ctx, a, id, operation, timeline, stream)
		if !errors.Is(err, core.ErrNotAvailable) || !handled {
			return path, handled, err
		}
		if ctx.Err() != nil {
			return "", true, ctx.Err()
		}
		if !r.clockReady() || r.o.MemberRole(ctx, r.o.Self, config.RoleServer) != nil {
			return "", true, core.ErrNotAvailable
		}
		_ = r.Tick(ctx)
		select {
		case <-ctx.Done():
			return "", true, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
func (r *Runtime) forwardNeutralControlOnce(ctx context.Context, a frontend.SessionAccess, id, operation string, timeline frontend.Timeline, stream frontend.StreamRequest) (string, bool, error) {
	if !r.policy(a.Protocol).DistributedBindings {
		return "", false, nil
	}
	if !r.Allocated(id) {
		return "", false, nil
	}
	if !r.clockReady() || r.o.MemberRole(ctx, r.o.Self, config.RoleServer) != nil {
		return "", true, core.ErrNotAvailable
	}
	c, err := r.CurrentCoordinator(ctx, id)
	if err != nil || !c.Deadline.After(time.Now().Add(r.o.ClockSkew)) {
		return "", true, core.ErrNotAvailable
	}
	if c.Identity == r.o.Self {
		return "", false, nil
	}
	if ctx.Value(neutralControlHop{}) != nil || a.Viewer == nil {
		return "", true, core.ErrNotAvailable
	}
	endpoint, tls, check, err := r.peer(ctx, c.Identity, NeutralControlPath)
	if err != nil {
		return "", true, core.ErrNotAvailable
	}
	transport, err := outbound.New(endpoint, tls, 500*time.Millisecond, check)
	if err != nil {
		return "", true, core.ErrNotAvailable
	}
	defer transport.CloseIdleConnections()
	raw, err := json.Marshal(neutralControlRequest{ID: id, Operation: operation, ActorID: a.Viewer.ID, Protocol: a.Protocol, Device: a.Device, Owner: a.Owner, Epoch: c.Epoch, Timeline: timeline, Stream: stream})
	if err != nil || len(raw) > 32<<10 {
		return "", true, core.ErrNotAvailable
	}
	q, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", true, core.ErrNotAvailable
	}
	client := &http.Client{Transport: transport, Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(q)
	if err != nil {
		return "", true, core.ErrNotAvailable
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", true, core.ErrNotAvailable
	}
	raw, err = io.ReadAll(io.LimitReader(response.Body, 8193))
	var reply neutralControlReply
	if err != nil || len(raw) > 8192 || json.Unmarshal(raw, &reply) != nil {
		return "", true, core.ErrNotAvailable
	}
	switch reply.Error {
	case "":
		return reply.Path, true, nil
	case "unauthorized":
		return "", true, core.ErrUnauthorized
	case "not-found":
		return "", true, core.ErrNotFound
	default:
		return "", true, core.ErrNotAvailable
	}
}

func (r *Runtime) neutralControlHTTP(w http.ResponseWriter, q *http.Request) {
	peer, err := r.o.Guard.Request(q)
	if err != nil || r.o.MemberRole(q.Context(), peer, config.RoleServer) != nil {
		http.Error(w, "denied", 401)
		return
	}
	if q.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	var request neutralControlRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, q.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF || request.ActorID <= 0 || request.ID == "" || !r.policy(request.Protocol).DistributedBindings {
		http.Error(w, "request", 400)
		return
	}
	c, err := r.CurrentCoordinator(q.Context(), request.ID)
	if err != nil || !r.clockReady() || c.Identity != r.o.Self || c.Epoch != request.Epoch || !c.Deadline.After(time.Now().Add(r.o.ClockSkew)) || r.o.MemberRole(q.Context(), r.o.Self, config.RoleServer) != nil {
		http.Error(w, "coordinator unavailable", 409)
		return
	}
	r.mu.Lock()
	control := r.neutralControl
	r.mu.Unlock()
	if control == nil {
		http.Error(w, "unavailable", 503)
		return
	}
	// The broker resolves the current actor and rechecks device, protocol, policy
	// and ownership under its owner gate. Never trust serialized viewer flags.
	path, err := control(context.WithValue(q.Context(), neutralControlHop{}, true), frontend.SessionAccess{Viewer: &core.Viewer{ID: request.ActorID}, Protocol: request.Protocol, Device: request.Device, Owner: request.Owner}, request.ID, request.Operation, request.Timeline, request.Stream)
	reply := neutralControlReply{Path: path}
	switch {
	case err == nil:
	case errors.Is(err, core.ErrUnauthorized):
		reply.Error = "unauthorized"
	case errors.Is(err, core.ErrNotFound):
		reply.Error = "not-found"
	default:
		reply.Error = "unavailable"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reply)
}
