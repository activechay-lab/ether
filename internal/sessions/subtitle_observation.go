package sessions

import (
	"context"
	"fmt"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func validObservedTrack(info core.MediaInfo, kind string, index int) bool {
	if kind == "subtitle" && index == -1 {
		return true
	}
	for _, stream := range info.Streams {
		if stream.Type == kind && stream.Index == index && index >= 0 {
			return true
		}
	}
	return false
}

// recordTrackCommand preserves the existing explicit manual command semantics.
// No automatic command is emitted; that requires a verified client mechanism.
func (b *Broker) recordTrackCommand(ctx context.Context, actor *core.Viewer, owner bool, target frontend.Session, cmd frontend.Command) error {
	access := frontend.SessionAccess{Viewer: actor, Owner: owner, Device: target.Device, Protocol: target.Protocol}
	s, _, err := b.access(ctx, access, target.ID)
	if err != nil {
		return err
	}
	unlock, err := b.gates.Acquire(ctx, fmt.Sprintf("broker/owner/%d/%s", s.public.ViewerID, s.clientID))
	if err != nil {
		return err
	}
	defer unlock()
	s, _, err = b.access(ctx, access, target.ID)
	if err != nil {
		return err
	}
	kind := "subtitle"
	if cmd.Kind == frontend.CommandSetAudio {
		kind = "audio"
	}
	ps, ok := b.deps.Playback.Session(s.playID)
	if !ok || ps == nil {
		return core.ErrSourceChanged
	}
	apply := func(info core.MediaInfo) error {
		if !validObservedTrack(info, kind, cmd.StreamIndex) {
			return core.ErrNotAvailable
		}
		if err := b.RecordAuthorizedSubtitleIntent(ctx, s.public.ViewerID, s.public.Item, true); err != nil {
			return err
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		live := b.sessions[s.public.ID]
		if live == nil || live.autoSubtitleIdentity != s.autoSubtitleIdentity || live.playID != s.playID {
			return core.ErrSourceChanged
		}
		live.manualSubtitle = true
		if live.autoSubtitleCancel != nil {
			live.autoSubtitleCancel()
		}
		return nil
	}
	if fence, ok := b.deps.Playback.(core.PlaybackSelectionFence); ok {
		return fence.WithSourceSelection(ctx, s.playID, core.SourceRevision(ps), apply)
	}
	// Keep legacy backends' explicit on-demand commands working. Automatic
	// eligibility remains denied unless a client/source path is qualified.
	return apply(ps.Info)
}

// observeSubtitleSelection runs after session authorization, sequence and stale
// report checks under the broker owner gate. Changed telemetry is a conservative
// automatic-work veto, NOT proof of manual intent: ordinary client progress also
// carries indices, and choosing off when already off is unobservable.
func (b *Broker) observeSubtitleSelection(ctx context.Context, s *brokerSession, t frontend.Timeline) error {
	if t.State == "stopped" || (s.public.Protocol != "jellyfin" && s.public.Protocol != "emby") || (t.AudioStreamIndex == nil && t.SubtitleStreamIndex == nil) {
		return nil
	}
	fence, ok := b.deps.Playback.(core.PlaybackSelectionFence)
	if !ok {
		return nil
	} // Legacy backends cannot safely interpret track telemetry.
	ps, ok := b.deps.Playback.Session(s.playID)
	if !ok || ps == nil {
		return core.ErrSourceChanged
	}
	revision := core.SourceRevision(ps)
	return fence.WithSourceSelection(ctx, s.playID, revision, func(info core.MediaInfo) error {
		if t.AudioStreamIndex != nil && !validObservedTrack(info, "audio", *t.AudioStreamIndex) {
			return core.ErrNotAvailable
		}
		if t.SubtitleStreamIndex != nil && !validObservedTrack(info, "subtitle", *t.SubtitleStreamIndex) {
			return core.ErrNotAvailable
		}
		audio, subtitle := s.opts.AudioStream, s.opts.SubtitleStream
		changed := (t.AudioStreamIndex != nil && *t.AudioStreamIndex != audio) ||
			(t.SubtitleStreamIndex != nil && *t.SubtitleStreamIndex != subtitle)
		// Existing commands are explicit manual actions and already record the
		// durable veto. Their later telemetry must not repeatedly write it.
		veto := changed && !s.manualSubtitle
		if veto {
			if err := b.RecordAuthorizedSubtitleIntent(ctx, s.public.ViewerID, s.public.Item, true); err != nil {
				return err
			}
			// Neither choices nor observations are advanced on a failed DB write.
			s.manualSubtitle = true
		}
		if t.AudioStreamIndex != nil {
			s.opts.AudioStream = *t.AudioStreamIndex
		}
		if t.SubtitleStreamIndex != nil {
			s.opts.SubtitleStream = *t.SubtitleStreamIndex
		}
		s.observedSourceRevision = revision
		b.mu.Lock()
		defer b.mu.Unlock()
		live := b.sessions[s.public.ID]
		if live == nil || live.autoSubtitleIdentity != s.autoSubtitleIdentity || live.playID != s.playID {
			return core.ErrSourceChanged
		}
		live.opts, live.manualSubtitle = s.opts, s.manualSubtitle
		live.observedSourceRevision = revision
		if veto && live.autoSubtitleCancel != nil {
			live.autoSubtitleCancel()
		}
		return nil
	})
}
