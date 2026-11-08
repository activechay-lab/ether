package playback

import (
	"context"
	"io"
	"strings"
	"sync"

	"github.com/braedonsaunders/ether/internal/core"
)

// SubtitleSource is a lease of one exact playing generation. Its context is
// cancelled on source retirement or session teardown. Close must be called even
// when no subtitle is obtained. This is not playback admission or authorization;
// only the authenticated session broker should acquire it after playing telemetry.
type SubtitleSource struct {
	Context    context.Context
	Session    *core.PlaySession
	InputURL   string
	manager    *Manager
	session    *session
	generation *generation
	revision   string
	once       sync.Once
}

// LeaseSubtitleSource never resolves, prepares, probes or reads source bytes.
func (m *Manager) LeaseSubtitleSource(ctx context.Context, id, revision string) (*SubtitleSource, error) {
	s := m.lookup(id)
	if s == nil || revision == "" {
		return nil, core.ErrSourceChanged
	}
	if err := lock(ctx, s.failSem); err != nil {
		return nil, err
	}
	defer unlock(s.failSem)
	ps := s.snapshot(m.now())
	if core.SourceRevision(ps) != revision {
		return nil, core.ErrSourceChanged
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.cur
	if s.closed || s.viewerState != "playing" || g == nil || !g.acquire() {
		return nil, core.ErrSourceChanged
	}
	if err := ctx.Err(); err != nil {
		g.release()
		return nil, err
	}
	return &SubtitleSource{Context: g.ctx, Session: ps, InputURL: m.urlFor(g), manager: m, session: s, generation: g, revision: revision}, nil
}

func (l *SubtitleSource) Close() {
	if l != nil {
		l.once.Do(func() { l.generation.release() })
	}
}

// Hold obtains a correction reader for this generation, never the session's
// replacement. Correction may call it later after waiting for a worker slot.
func (l *SubtitleSource) Hold() (io.ReaderAt, int64, func(), bool) {
	if l == nil || l.Context.Err() != nil || !l.generation.acquire() {
		return nil, 0, nil, false
	}
	g := l.generation
	if l.Context.Err() != nil {
		g.release()
		return nil, 0, nil, false
	}
	var once sync.Once
	return g.src, g.src.Size(), func() { once.Do(g.release) }, true
}

// Attach publishes a real cached text sidecar without restarting an encoder or
// changing a playback decision. The caller must hold its shared manual-choice
// fence through this call and any selection publication. This source fence
// makes that operation atomic relative to failover and teardown.
func (l *SubtitleSource) Attach(ctx context.Context, stream core.StreamInfo) (core.StreamInfo, error) {
	if l == nil || stream.Type != "subtitle" || !stream.External || !strings.HasPrefix(stream.ExternalURL, "ether-subtitle:") || (stream.Codec != "srt" && stream.Codec != "vtt" && stream.Codec != "subrip" && stream.Codec != "webvtt") {
		return core.StreamInfo{}, core.ErrNotAvailable
	}
	s := l.session
	if err := lock(ctx, s.failSem); err != nil {
		return core.StreamInfo{}, err
	}
	defer unlock(s.failSem)
	if l.Context.Err() != nil || core.SourceRevision(s.snapshot(l.manager.now())) != l.revision {
		return core.StreamInfo{}, core.ErrSourceChanged
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.cur != l.generation {
		return core.StreamInfo{}, core.ErrSourceChanged
	}
	if err := ctx.Err(); err != nil {
		return core.StreamInfo{}, err
	}
	g := l.generation
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.retired || g.closed {
		return core.StreamInfo{}, core.ErrSourceChanged
	}
	next := 0
	for _, st := range g.info.Streams {
		if st.Type == "subtitle" && st.External && st.ExternalURL == stream.ExternalURL {
			return st, nil
		}
		next = max(next, st.Index+1)
	}
	stream.Index = next
	g.info.Streams = append(g.info.Streams, stream)
	return stream, nil
}
