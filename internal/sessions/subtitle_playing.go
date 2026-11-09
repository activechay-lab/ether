package sessions

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// PlayingSubtitleSession identifies an authenticated playing session. It carries
// no media capability, provider secret or mutable source handle.
type PlayingSubtitleSession struct {
	Access                       frontend.SessionAccess
	ID, SourceID, SourceRevision string
	Item                         core.RatingKey
	Options                      core.DecideOptions
	ManualSubtitle               bool
}

// CurrentSubtitleSession revalidates viewer/session policy and gets the current
// selected options. Call it from a worker, never while holding a database fence.
func (b *Broker) CurrentSubtitleSession(ctx context.Context, a frontend.SessionAccess, id string) (PlayingSubtitleSession, error) {
	s, v, err := b.access(ctx, a, id)
	if err != nil {
		return PlayingSubtitleSession{}, err
	}
	if v.ID != s.public.ViewerID || !b.currentMediaPolicy(ctx, v, s) {
		return PlayingSubtitleSession{}, core.ErrUnauthorized
	}
	ps, ok := b.deps.Playback.Session(s.playID)
	if !ok || ps == nil || ps.UserID != v.ID || ps.Key != s.public.Item || ps.ViewerState != "playing" {
		return PlayingSubtitleSession{}, core.ErrSourceChanged
	}
	revision := core.SourceRevision(ps)
	if revision == "" {
		return PlayingSubtitleSession{}, core.ErrSourceChanged
	}
	return PlayingSubtitleSession{Access: frontend.SessionAccess{Viewer: v, Device: s.public.Device, Protocol: s.public.Protocol}, ID: id, SourceID: s.playID, SourceRevision: revision, Item: s.public.Item, Options: s.opts, ManualSubtitle: s.manualSubtitle}, nil
}

// enqueueSubtitleWork performs no network or SQL. It runs once per source generation of a broker
// session after accepted playing telemetry; a full worker window defers to the
// next report. Source lifetime fencing belongs to the worker's exact lease.
func (b *Broker) enqueueSubtitleWork(s *brokerSession) {
	if b.deps.OnActualPlaying == nil {
		return
	}
	epoch := uint64(1)
	if source, ok := b.deps.Playback.(interface{ SubtitleSourceEpoch(string) uint64 }); ok {
		epoch = source.SubtitleSourceEpoch(s.playID)
	}
	b.mu.Lock()
	current := b.sessions[s.public.ID]
	if b.closed || current == nil || current.autoSubtitleIdentity == nil || current.autoSubtitleIdentity != s.autoSubtitleIdentity || current.playID != s.playID || current.public.StartedAt != s.public.StartedAt || current.autoSubtitleQueued || current.autoSubtitleEpoch == epoch || epoch == 0 || b.autoSubtitleRunning >= 2 {
		b.mu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	current.autoSubtitleQueued = true
	current.autoSubtitleEpoch = epoch
	current.autoSubtitleCancel = cancel
	b.autoSubtitleRunning++
	b.autoSubtitleWG.Add(1)
	access := frontend.SessionAccess{Viewer: &core.Viewer{ID: s.public.ViewerID}, Device: s.public.Device, Protocol: s.public.Protocol}
	b.mu.Unlock()
	go func() {
		defer b.autoSubtitleWG.Done()
		defer cancel()
		defer func() {
			b.mu.Lock()
			b.autoSubtitleRunning--
			if live := b.sessions[s.public.ID]; live == current {
				live.autoSubtitleQueued = false
			}
			b.mu.Unlock()
		}()
		snapshot, err := b.CurrentSubtitleSession(ctx, access, s.public.ID)
		if err == nil {
			b.deps.OnActualPlaying(ctx, b, snapshot)
		}
	}()
}
