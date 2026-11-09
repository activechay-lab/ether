package playback

import (
	"context"
	"io"

	"github.com/braedonsaunders/ether/internal/core"
)

var _ core.PlaybackSourceResource = (*Manager)(nil)

// WithSourceResource acquires a read lease on the session's current opened
// generation, validates its source revision when provided, and invokes fn with
// a lifetime-canceled ReaderAt before releasing the generation reference.
func (m *Manager) WithSourceResource(ctx context.Context, id, revision string, fn func(ctx context.Context, src io.ReaderAt, size int64) error) error {
	if fn == nil {
		return core.ErrNotAvailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return core.ErrNotAvailable
	}
	s := m.sessions[id]
	if s == nil {
		m.mu.Unlock()
		return core.ErrNotFound
	}
	s.mu.Lock()
	g := s.cur
	lifetime := s.sources
	key := s.ident.key
	stopCtx := s.stopCtx
	if s.closed || g == nil || lifetime == nil {
		s.mu.Unlock()
		m.mu.Unlock()
		return core.ErrNotAvailable
	}
	lifetime.mu.Lock()
	g.mu.Lock()
	size := g.tmap.Size
	if size <= 0 && g.src != nil {
		size = g.src.Size()
	}
	obsRevision := g.observation.Revision
	valid := !lifetime.sealed && !g.retired && !g.closed && g.src != nil && size > 0
	if valid {
		g.refs++
	}
	g.mu.Unlock()
	lifetime.mu.Unlock()
	s.mu.Unlock()
	m.mu.Unlock()

	if !valid {
		return core.ErrNotAvailable
	}
	defer g.release()

	if revision != "" {
		currentRev := core.SourceRevision(&core.PlaySession{Key: key, Source: g.src})
		if currentRev != revision && obsRevision != revision {
			return core.ErrSourceChanged
		}
	}

	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopWatch := make(chan struct{})
	watchDone := make(chan struct{})
	if stopCtx != nil || g.ctx != nil {
		go func() {
			defer close(watchDone)
			var sDone, gDone <-chan struct{}
			if stopCtx != nil {
				sDone = stopCtx.Done()
			}
			if g.ctx != nil {
				gDone = g.ctx.Done()
			}
			select {
			case <-stopWatch:
			case <-sDone:
				cancel()
			case <-gDone:
				cancel()
			}
		}()
	} else {
		close(watchDone)
	}
	defer func() { close(stopWatch); <-watchDone }()
	if stopCtx != nil && stopCtx.Err() != nil {
		return stopCtx.Err()
	}
	if g.ctx != nil && g.ctx.Err() != nil {
		return g.ctx.Err()
	}

	reader := &sourceResourceReader{
		ctx:         readCtx,
		s:           s,
		g:           g,
		key:         key,
		revision:    revision,
		obsRevision: obsRevision,
		size:        size,
	}
	if err := fn(readCtx, reader, size); err != nil {
		return err
	}
	if err := readCtx.Err(); err != nil {
		return err
	}
	g.mu.Lock()
	retired := g.retired || g.closed
	g.mu.Unlock()
	if retired || s.isClosed() {
		return core.ErrSourceChanged
	}
	return nil
}

type sourceResourceReader struct {
	ctx         context.Context
	s           *session
	g           *generation
	key         core.RatingKey
	revision    string
	obsRevision string
	size        int64
}

func (r *sourceResourceReader) ReadAt(p []byte, off int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.s.isClosed() {
		return 0, core.ErrNotAvailable
	}
	r.g.mu.Lock()
	retired := r.g.retired || r.g.closed || r.g.src == nil
	r.g.mu.Unlock()
	if retired {
		return 0, core.ErrSourceChanged
	}
	if r.revision != "" {
		currentRev := core.SourceRevision(&core.PlaySession{Key: r.key, Source: r.g.src})
		if currentRev != r.revision && r.obsRevision != r.revision {
			return 0, core.ErrSourceChanged
		}
	}
	if off < 0 {
		return 0, core.ErrNotAvailable
	}
	if len(p) == 0 {
		return 0, nil
	}
	if off >= r.size {
		return 0, io.EOF
	}
	requested := len(p)
	if int64(len(p)) > r.size-off {
		p = p[:r.size-off]
	}
	var (
		n   int
		err error
	)
	if cr, ok := r.g.src.(core.ContextReaderAt); ok {
		n, err = cr.ReadAtContext(r.ctx, p, off)
	} else {
		n, err = r.g.src.ReadAt(p, off)
	}
	if n < requested && err == nil {
		err = io.EOF
	}
	return n, err
}
