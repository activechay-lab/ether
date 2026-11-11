package debrid

import (
	"context"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// Provider-issued hosts are candidates, not health evidence. Keep only
// authorities (never signed paths or tokens), scoped to the issuing account.
const (
	knownEdgeTTL           = 7 * 24 * time.Hour
	knownEdgeLimit         = 64
	knownEdgeWriteInterval = time.Hour
	knownEdgeStoreBudget   = 250 * time.Millisecond
)

func init() {
	store.Register("debrid", 4, `
CREATE TABLE debrid_cdn_edges (
  provider_id TEXT NOT NULL,
  host TEXT NOT NULL,
  seen_at INTEGER NOT NULL,
  PRIMARY KEY (provider_id, host)
);`)
}

type knownCDNEdges struct {
	mu    sync.Mutex
	hosts map[string]map[string]time.Time
}

func orderedCDNEdges(hosts map[string]time.Time, now time.Time) []string {
	out := make([]string, 0, len(hosts))
	for host, at := range hosts {
		if now.Sub(at) <= knownEdgeTTL {
			out = append(out, host)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := hosts[out[i]], hosts[out[j]]
		if a.Equal(b) {
			return out[i] < out[j]
		}
		return a.After(b)
	})
	if len(out) > knownEdgeLimit {
		out = out[:knownEdgeLimit]
	}
	return out
}

// rememberCDNEdge is called only on saved provider payloads. Merely
// receiving a URL is sufficient to learn a name; playback success is not
// required. Every later use still requires provider approval and a probe.
func (s *Service) rememberCDNEdge(ctx context.Context, p Payload) {
	if p.Kind != "torbox" || p.Provider == "" || p.IsDirect() || p.IsPluginProvider() || p.OnReplayEdge {
		return
	}
	u, err := url.Parse(p.URL)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return
	}
	now := s.now()
	s.cdnEdges.mu.Lock()
	if s.cdnEdges.hosts == nil {
		s.cdnEdges.hosts = make(map[string]map[string]time.Time)
	}
	hosts := s.cdnEdges.hosts[p.Provider]
	if hosts == nil {
		hosts = make(map[string]time.Time)
		s.cdnEdges.hosts[p.Provider] = hosts
	}
	last := hosts[u.Host]
	if !last.IsZero() && now.Sub(last) < knownEdgeWriteInterval {
		s.cdnEdges.mu.Unlock()
		return
	}
	hosts[u.Host] = now
	keep := orderedCDNEdges(hosts, now)
	trimmed := make(map[string]time.Time, len(keep))
	for _, host := range keep {
		trimmed[host] = hosts[host]
	}
	s.cdnEdges.hosts[p.Provider] = trimmed
	s.cdnEdges.mu.Unlock()
	if s.db == nil {
		return
	}
	dctx, cancel := context.WithTimeout(ctx, knownEdgeStoreBudget)
	defer cancel()
	_, err = s.db.ExecContext(dctx, `INSERT INTO debrid_cdn_edges(provider_id,host,seen_at) VALUES(?,?,?)
ON CONFLICT(provider_id,host) DO UPDATE SET seen_at=excluded.seen_at`, p.Provider, u.Host, now.Unix())
	if err != nil {
		s.log.Debug("debrid: remember CDN candidate", "err", err)
		return
	}
	// Bound storage as well as in-memory and per-wave work.
	_, err = s.db.ExecContext(dctx, `DELETE FROM debrid_cdn_edges WHERE provider_id=? AND
(seen_at < ? OR host NOT IN (SELECT host FROM debrid_cdn_edges WHERE provider_id=? ORDER BY seen_at DESC,host LIMIT ?))`,
		p.Provider, now.Add(-knownEdgeTTL).Unix(), p.Provider, knownEdgeLimit)
	if err != nil {
		s.log.Debug("debrid: prune CDN candidates", "err", err)
	}
}

func (s *Service) knownCDNHosts(ctx context.Context, provider string) []string {
	now := s.now()
	hosts := make(map[string]time.Time)
	s.cdnEdges.mu.Lock()
	for h, t := range s.cdnEdges.hosts[provider] {
		hosts[h] = t
	}
	s.cdnEdges.mu.Unlock()
	if s.db != nil {
		dctx, cancel := context.WithTimeout(ctx, knownEdgeStoreBudget)
		defer cancel()
		rows, err := s.db.QueryContext(dctx, `SELECT host,seen_at FROM debrid_cdn_edges WHERE provider_id=? AND seen_at>=? ORDER BY seen_at DESC,host LIMIT ?`,
			provider, now.Add(-knownEdgeTTL).Unix(), knownEdgeLimit)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var host string
				var at int64
				if rows.Scan(&host, &at) == nil {
					t := time.Unix(at, 0)
					if t.After(hosts[host]) {
						hosts[host] = t
					}
				}
			}
		}
	}
	return orderedCDNEdges(hosts, now)
}
