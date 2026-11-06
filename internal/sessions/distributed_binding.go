package sessions

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type neutralBindingStore interface {
	SaveNeutralBinding(context.Context, string, string, int64, string, []byte) error
	LoadNeutralBinding(context.Context, string) ([]byte, error)
	NeutralBindingIDs(context.Context, string, int64, string) ([]string, error)
	ClaimNeutralBinding(context.Context, string, string) error
	DeleteNeutralBinding(context.Context, string) error
	NeutralSessionCurrent(context.Context, string) bool
}
type neutralBinding struct {
	Version                                 int
	Public                                  frontend.Session
	Grant, PlayID, ClientID, PlaybackID     string
	Caps                                    core.ClientCaps
	Opts                                    core.DecideOptions
	Decision                                core.Decision
	AllowTranscoding                        *bool
	Format                                  frontend.StreamFormat
	StreamPosition, SegmentDuration         time.Duration
	Sequence                                uint64
	HistoryEpoch, HistorySequence           int64
	VersionHandle, ExactRelease, SourceETag string
	SourcePinned                            bool
	DeliveryToken, DeliveryGeneration       int64
	DeliverySession                         string
	Binding                                 Binding[frontend.PlayIntent]
	Source                                  core.PlaySession
}

func neutralProtocol(protocol frontend.Protocol) bool {
	return protocol == "jellyfin" || protocol == "emby"
}
func (b *Broker) saveNeutralBinding(ctx context.Context, id string, force bool) error {
	remote := b.deps.DistributedHLS
	storage, ok := remote.(neutralBindingStore)
	if !ok || !remote.Allocated(id) {
		return nil
	}
	b.mu.Lock()
	s := b.sessions[id]
	if s == nil || !neutralProtocol(s.public.Protocol) || s.live || s.format != frontend.StreamHLS {
		b.mu.Unlock()
		return nil
	}
	if !force && !s.distributedSaved.IsZero() && b.deps.Now().Sub(s.distributedSaved) < 30*time.Second {
		b.mu.Unlock()
		return nil
	}
	snapshot := neutralBinding{Version: 1, Public: s.public, Grant: s.grant, PlayID: s.playID, ClientID: s.clientID, PlaybackID: s.playbackID, Caps: s.caps, Opts: s.opts, Decision: s.decision, AllowTranscoding: s.allowTranscoding, Format: s.format, StreamPosition: s.streamPosition, SegmentDuration: s.segmentDuration, Sequence: s.sequence, HistoryEpoch: s.historyEpoch, HistorySequence: s.historySequence, VersionHandle: s.versionHandle, ExactRelease: s.exactRelease, SourcePinned: s.sourcePinned, SourceETag: s.sourceETag, DeliveryToken: s.deliveryToken, DeliveryGeneration: s.deliveryGeneration, DeliverySession: s.deliverySession}
	b.mu.Unlock()
	binding, ok := b.bindings.Get(id)
	if !ok || !binding.Started {
		return core.ErrNotAvailable
	}
	snapshot.Binding = *binding
	source, ok := b.deps.Playback.Session(snapshot.PlayID)
	if !ok || source == nil {
		return core.ErrNotAvailable
	}
	snapshot.Source = *source
	snapshot.Source.Source = nil
	raw, err := json.Marshal(snapshot)
	if err != nil || len(raw) > 256<<10 {
		return core.ErrNotAvailable
	}
	if err = storage.SaveNeutralBinding(ctx, id, string(snapshot.Public.Protocol), snapshot.Public.ViewerID, snapshot.Public.Device.ID, raw); err != nil {
		return err
	}
	b.mu.Lock()
	if s := b.sessions[id]; s != nil {
		s.distributed = true
		s.distributedSaved = b.deps.Now()
	}
	b.mu.Unlock()
	return nil
}
func (b *Broker) neutralCurrent(ctx context.Context, s *brokerSession) bool {
	if !s.distributed {
		return true
	}
	storage, ok := b.deps.DistributedHLS.(neutralBindingStore)
	return ok && storage.NeutralSessionCurrent(ctx, s.public.ID)
}

// Authentication facts may be restored, but no source ownership, encoder,
// preparation, plan capacity or new admission is synthesized from a snapshot.
func (b *Broker) restoreNeutralBinding(ctx context.Context, id string, access *frontend.SessionAccess, grant, protocol string) error {
	return b.restoreNeutralBindingMode(ctx, id, access, grant, protocol, false)
}
func (b *Broker) restoreNeutralBindingMode(ctx context.Context, id string, access *frontend.SessionAccess, grant, protocol string, media bool) error {
	storage, ok := b.deps.DistributedHLS.(neutralBindingStore)
	if !ok {
		return core.ErrNotFound
	}
	b.mu.Lock()
	present := b.sessions[id] != nil
	closed := b.closed
	b.mu.Unlock()
	if closed {
		return core.ErrNotAvailable
	}
	if present {
		return nil
	}
	raw, err := storage.LoadNeutralBinding(ctx, id)
	if err != nil {
		return core.ErrNotFound
	}
	var snapshot neutralBinding
	if len(raw) > 256<<10 || json.Unmarshal(raw, &snapshot) != nil || snapshot.Version != 1 {
		return core.ErrNotAvailable
	}
	p := snapshot.Public
	if p.ID != id || !neutralProtocol(p.Protocol) || p.ViewerID <= 0 || p.Device.ID == "" || snapshot.PlayID == "" || snapshot.Grant == "" || snapshot.Format != frontend.StreamHLS || !snapshot.Binding.Started || snapshot.Binding.ID != id || snapshot.Binding.PlayID != snapshot.PlayID || snapshot.Binding.UserID != p.ViewerID || snapshot.Binding.ClientID != snapshot.ClientID || snapshot.Binding.Key != p.Item || snapshot.Source.ID != snapshot.PlayID || snapshot.Source.UserID != p.ViewerID || snapshot.Source.Key != p.Item || snapshot.ClientID != clientKey(p.Protocol, p.Device) {
		return core.ErrUnauthorized
	}
	if protocol != "" && string(p.Protocol) != protocol {
		return core.ErrUnauthorized
	}
	if access != nil {
		viewer, e := b.viewer(ctx, access.Viewer)
		if e != nil {
			return e
		}
		owner := access.Owner && viewer.IsOwner && !viewer.Delegated
		if access.Owner && !owner || !owner && (viewer.ID != p.ViewerID || access.Protocol != p.Protocol || access.Device.ID == "" || access.Device.ID != p.Device.ID) {
			return core.ErrUnauthorized
		}
	} else if subtle.ConstantTimeCompare([]byte(grant), []byte(snapshot.Grant)) != 1 {
		return core.ErrUnauthorized
	}
	viewer, err := b.deps.ActiveViewer(ctx, p.ViewerID)
	if err != nil || viewer == nil || viewer.ID != p.ViewerID {
		return core.ErrUnauthorized
	}
	s := &brokerSession{public: p, grant: snapshot.Grant, playID: snapshot.PlayID, clientID: snapshot.ClientID, playbackID: snapshot.PlaybackID, caps: snapshot.Caps, opts: snapshot.Opts, decision: snapshot.Decision, allowTranscoding: snapshot.AllowTranscoding, format: snapshot.Format, streamPosition: snapshot.StreamPosition, segmentDuration: snapshot.SegmentDuration, sequence: snapshot.Sequence, historyEpoch: snapshot.HistoryEpoch, historySequence: snapshot.HistorySequence, versionHandle: snapshot.VersionHandle, exactRelease: snapshot.ExactRelease, sourcePinned: snapshot.SourcePinned, sourceETag: snapshot.SourceETag, deliveryToken: snapshot.DeliveryToken, deliveryGeneration: snapshot.DeliveryGeneration, deliverySession: snapshot.DeliverySession, distributed: true, restored: true}
	if !b.currentMediaPolicy(ctx, viewer, s) {
		return core.ErrUnauthorized
	}
	if !storage.NeutralSessionCurrent(ctx, id) && !(media && b.neutralMediaCurrent(ctx, s)) {
		return core.ErrNotAvailable
	}
	if hs, ok := b.deps.DistributedHLS.Session(id); !ok || hs == nil {
		return core.ErrNotAvailable
	}
	restore, ok := b.deps.Playback.(interface {
		RestoreNeutralSource(context.Context, string, core.PlaySession) error
	})
	if !ok {
		return core.ErrNotAvailable
	}
	if err = restore.RestoreNeutralSource(ctx, id, snapshot.Source); err != nil {
		return err
	}
	if b.cluster != nil && !media {
		if err = storage.ClaimNeutralBinding(ctx, id, b.cluster.store.node); err != nil {
			return err
		}
	}
	unlock, err := b.gates.Acquire(ctx, "broker/restore/"+id)
	if err != nil {
		return err
	}
	defer unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return core.ErrNotAvailable
	}
	if b.sessions[id] != nil {
		return nil
	}
	if len(b.sessions) >= 4096 {
		return core.ErrNotAvailable
	}
	b.sessions[id] = s
	b.bindings.Put(&snapshot.Binding)
	b.owners.BindFor("broker", p.ViewerID, s.clientID, p.Item, s.playbackID, s.playID, true, b.deps.Now())
	lease := b.sourceLeases[s.playID]
	if lease == nil {
		lease = &sourceLease{}
		b.sourceLeases[s.playID] = lease
	}
	lease.sessions++
	return nil
}
func (b *Broker) restoreNeutralScope(ctx context.Context, a frontend.SessionAccess) error {
	storage, ok := b.deps.DistributedHLS.(neutralBindingStore)
	if !ok || !neutralProtocol(a.Protocol) || a.Viewer == nil || a.Device.ID == "" {
		return nil
	}
	ids, err := storage.NeutralBindingIDs(ctx, string(a.Protocol), a.Viewer.ID, a.Device.ID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = b.restoreNeutralBinding(ctx, id, &a, "", string(a.Protocol)); err != nil && err != core.ErrNotFound && err != core.ErrNotAvailable {
			return err
		}
	}
	return nil
}

func (b *Broker) neutralMediaCurrent(ctx context.Context, s *brokerSession) bool {
	if !s.distributed {
		return true
	}
	storage, ok := b.deps.DistributedHLS.(interface {
		NeutralMediaCurrent(context.Context, string) bool
	})
	return ok && storage.NeutralMediaCurrent(ctx, s.public.ID)
}
