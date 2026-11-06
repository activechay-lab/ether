package nodeinput

import (
	"context"
	"github.com/braedonsaunders/ether/internal/nodesource"
)

// readAt serializes source changes with renewal. No bytes from a failed or
// superseded operation escape: the complete undelivered chunk is requested
// again at the exact offset. Network operations honor the shorter stall bound.
func (in *Input) readAt(ctx context.Context, p []byte, off int64) (int, error) {
	g := in.g
	if g.o.Recover != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.o.RequestTimeout)
		defer cancel()
	}
	for {
		in.readMu.RLock()
		g.mu.Lock()
		reader, old := in.reader, in.grant
		sealed := in.sealed
		g.mu.Unlock()
		if ctx.Err() != nil {
			in.readMu.RUnlock()
			return 0, ctx.Err()
		}
		if sealed || reader == nil {
			in.readMu.RUnlock()
			return 0, ErrDenied
		}
		attempt, cancel := context.WithTimeout(ctx, g.o.ReadTimeout)
		n, err := reader.ReadAtContext(attempt, p, off)
		expired := attempt.Err() != nil
		cancel()
		in.readMu.RUnlock()
		if err == nil && !expired && n == len(p) {
			return n, nil
		}
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		err = in.replaceSource(ctx, reader, old)
		if err != nil {
			in.readMu.RLock()
			retry := g.o.Recover != nil && in.recoveries < g.o.MaxRecoveries
			in.readMu.RUnlock()
			if retry && ctx.Err() == nil {
				continue
			}
			return 0, err
		}
	}
}

func (in *Input) replaceSource(ctx context.Context, reader *nodesource.Reader, old nodesource.Grant) error {
	in.readMu.Lock()
	defer in.readMu.Unlock()
	g := in.g
	g.mu.Lock()
	current, sealed, grant := in.reader, in.sealed, in.grant
	g.mu.Unlock()
	if sealed {
		return ErrDenied
	}
	if current != reader || grant != old {
		return nil
	}
	if g.o.Recover == nil || in.recoveries >= g.o.MaxRecoveries {
		return ErrDenied
	}
	in.recoveries++
	// Re-grant must fence the old source in shared authority before returning.
	c, next, err := g.o.Recover(ctx, old)
	if err != nil || c == nil {
		if c != nil {
			c.Close()
		}
		return ErrDenied
	}
	if next.Scope.Fence != old.Scope.Fence || next.Scope.Viewer != old.Scope.Viewer || next.Scope.Revision != old.Scope.Revision || next.Scope.SourceGeneration <= old.Scope.SourceGeneration || next.Expires.Before(old.Expires) {
		c.Close()
		return ErrDenied
	}
	replacement, err := c.Open(ctx, next)
	if err != nil || replacement.Size() != in.size || replacement.ReleaseID() != old.Scope.Revision {
		if replacement != nil {
			_ = replacement.Close()
		}
		c.Close()
		return ErrDenied
	}
	g.mu.Lock()
	if in.sealed {
		g.mu.Unlock()
		_ = replacement.Close()
		c.Close()
		return ErrDenied
	}
	_ = reader.Close()
	in.reader, in.grant, in.scope = replacement, next, next.Scope
	in.ownedClients = append(in.ownedClients, c)
	g.mu.Unlock()
	return nil
}
