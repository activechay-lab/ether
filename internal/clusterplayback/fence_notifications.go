package clusterplayback

import (
	"context"
	"github.com/braedonsaunders/ether/internal/clusterbus"
	"time"
)

// Notifications are invalidation hints, never authority. Lost events are covered
// by generationfence.MaxStaleness. Subscribe per active HTTP request, and remove
// on handler exit; callbacks only update an atomic counter, never query the DB.
const topicPlaybackRoute = "playback.route"

func (r *Runtime) watchAuthority(session string, invalidate func()) func() {
	if r.o.Bus == nil {
		return func() {}
	}
	a := r.o.Bus.Subscribe(topicPlaybackRoute, func(p []byte) {
		if string(p) == session {
			invalidate()
		}
	})
	b := r.o.Bus.Subscribe(clusterbus.TopicResync, func([]byte) { invalidate() })
	return func() { a(); b() }
}
func (r *Runtime) routeChanged(ctx context.Context, session string) {
	if r.o.Bus == nil {
		return
	}
	c, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	// A failed notification cannot roll back a committed route. The bounded
	// periodic check is mandatory even when a bus is present.
	_ = r.o.Bus.Publish(c, topicPlaybackRoute, []byte(session))
}
