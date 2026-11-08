package playback

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

var _ core.PlaybackSelectionFence = (*Manager)(nil)

// WithSourceSelection holds the existing source transition fence before fn may
// acquire a manual-intent database writer. No bytes are read or source prepared.
func (m *Manager) WithSourceSelection(ctx context.Context, id, revision string, fn func(core.MediaInfo) error) error {
	s := m.lookup(id)
	if s == nil || revision == "" || fn == nil {
		return core.ErrSourceChanged
	}
	if err := lock(ctx, s.failSem); err != nil {
		return err
	}
	defer unlock(s.failSem)
	ps := s.snapshot(m.now())
	if core.SourceRevision(ps) != revision {
		return core.ErrSourceChanged
	}
	s.mu.Lock()
	closed := s.closed || s.cur == nil
	s.mu.Unlock()
	if closed {
		return core.ErrSourceChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn(ps.Info)
}
