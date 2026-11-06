package remotetranscode

import (
	"context"
	"time"
)

// Attach observes an already admitted execution. It sends only status, never
// Start, and requires an exact identity under the caller's current authorization.
func (a *Adapter) Attach(ctx context.Context, id Identity) (*Session, error) {
	if !validIdentity(id) {
		return nil, ErrUnsupported
	}
	a.mu.Lock()
	s := a.sessions[id]
	if s == nil {
		if len(a.sessions) >= a.opts.Capacity {
			a.mu.Unlock()
			return nil, ErrUncertain
		}
		s = &Session{adapter: a, id: id, done: make(chan struct{})}
		a.sessions[id] = s
	}
	a.mu.Unlock()
	response, err := a.call(ctx, message{Op: "status", Identity: id}, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if err = s.observe(response); err != nil {
		return nil, err
	}
	s.mu.Lock()
	live := s.state == "running" && !s.stopped && s.lease.Deadline.After(time.Now())
	s.mu.Unlock()
	if !live {
		return nil, ErrUncertain
	}
	s.touch()
	return s, nil
}
