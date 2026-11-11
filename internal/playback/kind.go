package playback

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

var _ core.KindPlayback = (*Manager)(nil)

// kindIdentity names the synthetic release a plugin kind item opens under, so
// session accounting, snapshots and logs have a stable, non-empty source id.
func kindIdentity(in core.KindInput) string {
	if in.SourceID != "" {
		return "kind:" + in.SourceID
	}
	if in.Title != nil {
		return "kind:" + in.Title.Key.String()
	}
	return "kind:item"
}

// PrepareKind opens a non-catalogue plugin item through the same session,
// decision and stream-limit machinery as a catalogue title (spec §15.1). The
// item's bytes come from KindInput.Open; everything after that — probe,
// direct-play/remux/transcode decision, HLS/DASH output, stream limits and
// session accounting — is the shared pipeline.
func (m *Manager) PrepareKind(ctx context.Context, user *core.User, in core.KindInput, caps core.ClientCaps) (*core.PlaySession, error) {
	if user == nil {
		return nil, core.ErrUnauthorized
	}
	if in.Title == nil || in.Open == nil {
		return nil, core.ErrNotAvailable
	}
	id := identity{userID: user.ID, clientID: caps.ClientID, key: in.Title.Key, surface: playbackSurface(string(caps.Protocol))}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("playback: shutting down")
	}
	// Reuse a live session for the same viewer, client and item.
	if s := m.byIdent[id]; s != nil {
		m.mu.Unlock()
		s.touch(m.now())
		return s.snapshot(m.now()), nil
	}
	fk := flightKey{id: id}
	f := m.flights[fk]
	if f == nil {
		parent := context.WithoutCancel(ctx)
		wctx, cancel := context.WithTimeout(parent, m.opts.PrepareTimeout)
		kindCopy := in
		f = &flight{sources: newSourceLifetime(user.ID, string(caps.Protocol), caps.ClientID), done: make(chan struct{}), cancel: cancel, kind: &kindCopy}
		m.flights[fk] = f
		u := *user
		go m.runFlight(wctx, f, fk, &u, caps)
	}
	f.waiters++
	f.playRequest = true
	m.mu.Unlock()

	select {
	case <-f.done:
		if f.err != nil {
			return nil, f.err
		}
		ps, _, err := m.snapshotForAdmission(ctx, f.sess, caps, time.Now(), 0)
		return ps, err
	case <-ctx.Done():
		m.mu.Lock()
		if f.waiters--; f.waiters == 0 && !f.detached {
			m.abandonFlightLocked(fk, f)
		}
		m.mu.Unlock()
		return nil, ctx.Err()
	}
}

// SessionForKind finds the live kind session for a viewer, client and item.
func (m *Manager) SessionForKind(userID int64, clientID string, key core.RatingKey) (*core.PlaySession, bool) {
	for _, surface := range []string{"", playbackSurface("plex"), playbackSurface("jellyfin"), playbackSurface("emby")} {
		m.mu.Lock()
		s := m.byIdent[identity{userID: userID, clientID: clientID, key: key, surface: surface}]
		m.mu.Unlock()
		if s != nil && s.kindOpen != nil {
			return s.snapshot(m.now()), true
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.byIdent {
		if s != nil && s.kindOpen != nil && s.ident.userID == userID && s.ident.clientID == clientID && s.ident.key == key {
			return s.snapshot(m.now()), true
		}
	}
	return nil, false
}

// openKindInitial opens a plugin item's ByteSource-backed MediaSource and
// probes it into a generation. It bypasses resolution and failover: a kind
// source is a single, resolved stream (spec §15.1).
func (m *Manager) openKindInitial(ctx context.Context, s *session) (*generation, error) {
	if s.kindOpen == nil {
		return nil, fmt.Errorf("playback: session %s is not a kind session", s.id)
	}
	src, err := s.kindOpen(ctx)
	if err != nil {
		return nil, fmt.Errorf("playback: open kind source: %w", err)
	}
	if src == nil {
		return nil, errors.New("playback: kind source returned no media")
	}
	if src.Size() <= 0 {
		_ = src.Close()
		return nil, errors.New("playback: kind source is empty")
	}
	finish := func(closeErr error) { _ = src.Close() }
	res := &core.Resolution{
		Title:      s.ident.key,
		ResolvedAt: m.now(),
		Release: core.Release{
			ID:       kindIdentity(core.KindInput{SourceID: s.kindSourceID, Title: s.title}),
			Name:     displayTitle(s.title),
			Protocol: core.ProtocolHTTP,
			PubDate:  m.now(),
		},
	}
	fallback := func(size int64) core.MediaInfo {
		info := EstimateInfo(s.title, s.caps, m.selectProfile(s.caps, &s.user, s.ident.key))
		info.Size = size
		return info
	}
	return m.probeGeneration(ctx, s, res, src, finish, fallback)
}
