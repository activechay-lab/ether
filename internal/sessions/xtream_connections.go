package sessions

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.RegisterAfter("xtream_connections", "sessions")
	store.Register("xtream_connections", 1, `CREATE TABLE sessions_xtream_connections (
 grant_reference TEXT PRIMARY KEY,
 viewer_id INTEGER NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
 target TEXT NOT NULL,
 peer TEXT NOT NULL,
 expires INTEGER NOT NULL
 ); CREATE INDEX sessions_xtream_connections_expiry ON sessions_xtream_connections(expires);`)
	store.RegisterViewerTable("xtream_connections", "sessions_xtream_connections", "viewer_id", store.ViewerMergeDrop)
}

// acquireXtreamConnection enforces the advertised one-connection login limit
// across nodes. Range requests and HLS segments for the same peer and item share
// a slot. A competing peer or item is refused until the current stream is idle.
// Only non-secret grant references and hashed peer/target bindings are persisted.
func (s *lazyStreams) acquireXtreamConnection(r *http.Request, reference string, viewer int64, target string) (context.Context, func(), error) {
	peer, target, err := xtreamBinding(r, target)
	if err != nil {
		return nil, nil, err
	}
	const ttl = 30 * time.Second
	update := func(ctx context.Context) error {
		now := s.broker.deps.Now().UnixMilli()
		result, err := s.db.ExecContext(ctx, `INSERT INTO sessions_xtream_connections(grant_reference,viewer_id,target,peer,expires) VALUES(?,?,?,?,?) ON CONFLICT(grant_reference) DO UPDATE SET target=excluded.target,peer=excluded.peer,expires=excluded.expires WHERE sessions_xtream_connections.target=excluded.target OR sessions_xtream_connections.expires<=?`, reference, viewer, target, peer, now+ttl.Milliseconds(), now)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("connection limit")
		}
		return nil
	}
	if err := update(r.Context()); err != nil {
		return nil, nil, err
	}
	// Renew long file responses as well as HLS segment activity. Cancellation
	// also stops the goroutine; Close waits for it so no process work is orphaned.
	ctx, cancel := context.WithCancel(r.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if update(ctx) != nil {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(); <-done }, nil
}

func (s *lazyStreams) AppConnections(ctx context.Context, credential string) (int, error) {
	if s.protocol != "xtream" {
		return 0, fmt.Errorf("unavailable")
	}
	ref, viewer, err := s.grants.GrantReference(ctx, "xtream", credential)
	if err != nil || viewer == nil {
		return 0, fmt.Errorf("unauthorized")
	}
	var count int
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions_xtream_connections WHERE grant_reference=? AND viewer_id=? AND expires>?`, ref, viewer.ID, s.broker.deps.Now().UnixMilli()).Scan(&count)
	return count, err
}

func xtreamBinding(r *http.Request, target string) (string, string, error) {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if ip, ok := frontend.ClientIP(r.Context()); ok {
		peer = ip.String()
		err = nil
	}
	if err != nil {
		return "", "", fmt.Errorf("invalid stream peer")
	}
	return lazyHash(peer), lazyHash(peer + "/" + target), nil
}

// PrepareAppConnection is called only after primary-URL authentication and
// item/channel policy. A switch from the same peer replaces the previous
// playback; stale segments can renew only their original target, never switch.
func (s *lazyStreams) PrepareAppConnection(r *http.Request, credential string, req frontend.PlanRequest) error {
	if s.protocol != "xtream" || req.Viewer == nil || req.Protocol != s.protocol {
		return core.ErrUnauthorized
	}
	ref, v, err := s.grants.GrantReference(r.Context(), "xtream", credential)
	if err != nil || v == nil || v.ID != req.Viewer.ID {
		return core.ErrUnauthorized
	}
	peer, target, err := xtreamBinding(r, fmt.Sprint(req.Item)+"/"+req.Channel)
	if err != nil {
		return err
	}
	now := s.broker.deps.Now().UnixMilli()
	result, err := s.db.ExecContext(r.Context(), `INSERT INTO sessions_xtream_connections(grant_reference,viewer_id,target,peer,expires) VALUES(?,?,?,?,?) ON CONFLICT(grant_reference) DO UPDATE SET target=excluded.target,peer=excluded.peer,expires=excluded.expires WHERE sessions_xtream_connections.peer=excluded.peer OR sessions_xtream_connections.expires<=?`, ref, v.ID, target, peer, now+int64((30*time.Second)/time.Millisecond), now)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return core.ErrStreamLimit
	}
	access := frontend.SessionAccess{Viewer: v, Device: req.Device, Protocol: s.protocol}
	streams, err := s.broker.Sessions(r.Context(), access)
	if err != nil {
		return err
	}
	for _, stream := range streams {
		if stream.Device.ID == req.Device.ID && (stream.Item != req.Item || stream.Channel != req.Channel) {
			if err := s.broker.Stop(r.Context(), access, stream.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
