package clusterplayback

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"time"
)

func (r *Runtime) sourceActivity(ctx context.Context, peer nodewire.Identity, m sourceRequest) (sourceResponse, error) {
	if m.Prepare == nil {
		return sourceResponse{}, ErrDenied
	}
	prepared, e := r.preparationCurrent(ctx, peer, m.Prepare.Operation)
	if e != nil {
		return sourceResponse{}, e
	}
	switch m.Op {
	case "prepare-open":
		if !r.o.Playback.MarkSurfaceOpened(prepared.PlayID) {
			return sourceResponse{}, ErrDenied
		}
	case "prepare-hidden":
		if !r.o.Playback.MarkSurfacePrepared(prepared.PlayID) {
			return sourceResponse{}, ErrDenied
		}
	case "prepare-activity":
		if m.Position < 0 || (m.State != "playing" && m.State != "paused") {
			return sourceResponse{}, ErrDenied
		}
		ps, ok := r.o.Playback.Session(prepared.PlayID)
		if !ok || ps == nil {
			return sourceResponse{}, ErrDenied
		}
		// Values selecting the source come only from the exact validated snapshot.
		r.o.Playback.PlaybackProgress(ps.UserID, ps.ClientID, ps.Key, m.Position, m.State)
	default:
		return sourceResponse{}, ErrDenied
	}
	return sourceResponse{Prepared: prepared}, nil
}

// OpenRemoteSource runs outside the broker mutex, before local visibility commit.
func (p *RemotePlayback) OpenRemoteSource(ctx context.Context, id string) error {
	p.mu.Lock()
	entry := p.entries[id]
	if entry == nil || entry.retiring || entry.retired || entry.snapshot == nil {
		p.mu.Unlock()
		return ErrDenied
	}
	owner, operation := entry.owner, entry.operation
	p.mu.Unlock()
	_, err := p.r.sourceCall(ctx, Route{Source: owner}, sourceRequest{Op: "prepare-open", Prepare: &PrepareRequest{Operation: operation}})
	return err
}

// Touch and PlaybackProgress enqueue bounded state; Tick owns network work.
func (p *RemotePlayback) Touch(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.entries[id]; e != nil && !e.retiring && !e.retired {
		e.activity = time.Now()
		if e.state == "" {
			e.state = "playing"
		}
	}
}
func (p *RemotePlayback) PlaybackProgress(viewer int64, client string, key core.RatingKey, position time.Duration, state string) {
	if position < 0 || (state != "playing" && state != "paused") {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.entries {
		if e.snapshot != nil && !e.retiring && !e.retired && e.snapshot.UserID == viewer && e.snapshot.ClientID == client && e.snapshot.Key == key {
			e.position = position
			e.state = state
			e.activity = time.Now()
		}
	}
}
func (p *RemotePlayback) tickActivity(ctx context.Context) error {
	type pending struct {
		entry            *remotePreparation
		owner            nodewire.Identity
		operation, state string
		position         time.Duration
		activity         time.Time
		hide             bool
		mode             uint64
	}
	p.mu.Lock()
	var list []pending
	for _, e := range p.entries {
		if e.retired || e.retiring || e.preparing || e.snapshot == nil {
			continue
		}
		if e.mode > e.sentMode || e.activity.After(e.sentActivity) {
			list = append(list, pending{entry: e, owner: e.owner, operation: e.operation, state: e.state, position: e.position, activity: e.activity, hide: e.snapshot.PreparedOnly, mode: e.mode})
		}
	}
	p.mu.Unlock()
	var result error
	for _, item := range list {
		op := "prepare-open"
		if item.hide {
			op = "prepare-hidden"
		}
		if _, err := p.r.sourceCall(ctx, Route{Source: item.owner}, sourceRequest{Op: op, Prepare: &PrepareRequest{Operation: item.operation}}); err != nil {
			result = err
			continue
		}
		if !item.hide && !item.activity.IsZero() {
			if _, err := p.r.sourceCall(ctx, Route{Source: item.owner}, sourceRequest{Op: "prepare-activity", Prepare: &PrepareRequest{Operation: item.operation}, Position: item.position, State: item.state}); err != nil {
				result = err
				continue
			}
		}
		p.mu.Lock()
		item.entry.sentMode = item.mode
		item.entry.sentActivity = item.activity
		p.mu.Unlock()
	}
	return result
}
