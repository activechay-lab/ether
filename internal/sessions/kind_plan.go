package sessions

import (
	"context"
	"crypto/rand"
	"fmt"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// planKind resolves a plugin kind item into a plan through the shared
// KindPlayback pipeline (spec §15.1). It mirrors the catalogue tail of Plan:
// reserve, gate, prepare, decide, store. Catalogue-only concepts (versions,
// detail sources, release ETags) do not apply and are rejected by the caller.
func (b *Broker) planKind(ctx context.Context, req frontend.PlanRequest, v *core.Viewer) (frontend.PlayPlan, error) {
	if !b.reservePlan(v.ID) {
		return frontend.PlayPlan{}, core.ErrStreamLimit
	}
	defer b.releasePlanReservation(v.ID)
	unlock, err := b.gates.Acquire(ctx, fmt.Sprintf("broker/owner/%d/%s", v.ID, req.Caps.ClientID))
	if err != nil {
		return frontend.PlayPlan{}, err
	}
	defer unlock()

	kindPlayback, ok := b.deps.Playback.(core.KindPlayback)
	if !ok {
		return frontend.PlayPlan{}, core.ErrNotAvailable
	}
	in, err := b.deps.KindSource(ctx, v, req.Item)
	if err != nil || in == nil {
		return frontend.PlayPlan{}, core.ErrNotFound
	}
	if in.Title == nil || in.Title.Key != req.Item {
		return frontend.PlayPlan{}, core.ErrNotFound
	}
	requested := b.deps.Now()
	ps, err := kindPlayback.PrepareKind(ctx, v, *in, req.Caps)
	if err != nil {
		return frontend.PlayPlan{}, err
	}
	if ps == nil {
		return frontend.PlayPlan{}, core.ErrNotAvailable
	}
	existed := false
	if prev, ok := kindPlayback.SessionForKind(v.ID, req.Caps.ClientID, req.Item); ok && prev != nil && prev.ID == ps.ID {
		existed = true
	}
	b.acquirePlanSource(ps.ID, !existed)
	stored := false
	defer func() {
		if !stored {
			b.releasePlanSource(ps.ID, false)
		}
	}()

	if b.deps.SelectScopedTracks != nil || b.deps.SelectTracks != nil {
		var selected core.DecideOptions
		var terr error
		if b.deps.SelectScopedTracks != nil {
			selected, terr = b.deps.SelectScopedTracks(ctx, v, req.Device, in.Title, ps.Info, req.Intent.Options, req.Intent.AudioSet, req.Intent.SubtitleSet)
		} else {
			selected, terr = b.deps.SelectTracks(ctx, v, in.Title, ps.Info, req.Intent.Options, req.Intent.AudioSet, req.Intent.SubtitleSet)
		}
		if terr != nil {
			return frontend.PlayPlan{}, terr
		}
		req.Intent.Options = selected
	}
	dec, err := b.decideProfile(ps, req)
	if err != nil {
		return frontend.PlayPlan{}, err
	}
	if req.Intent.AllowTranscoding != nil && !*req.Intent.AllowTranscoding && decisionEncodes(dec, ps.Info) {
		return frontend.PlayPlan{}, core.ErrNotAvailable
	}
	if (dec.Kind == core.DecisionDirectPlay && !req.Intent.Options.DirectPlay) || (dec.Kind == core.DecisionRemux && !req.Intent.Options.DirectStream) {
		return frontend.PlayPlan{}, core.ErrNotAvailable
	}
	id := rand.Text()
	etag := sourceRevision(ps)
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return frontend.PlayPlan{}, core.ErrNotAvailable
	}
	b.plans[id] = brokerPlan{request: req, source: ps, decision: dec, created: b.deps.Now(), requested: requested, etag: etag}
	stored = true
	b.mu.Unlock()
	direct, remux, transcoding := b.planAlternatives(ps, req)
	hls, hlsContainer := b.hlsPlanOutput(ps, req, dec)
	return frontend.PlayPlan{ID: id, Media: publicMedia(ps.Info), Decision: dec, DirectPlay: direct, DirectStream: remux, Transcoding: transcoding, HLS: hls, HLSContainer: hlsContainer, ETag: etag}, nil
}
