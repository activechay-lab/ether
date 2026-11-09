package resolver

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

const failedHealthTTL = 2 * time.Minute

// Failed sources are skipped briefly, so retries reach lower-ranked releases
// instead of probing the same DMCA'd articles again. This is not MarkBad: a new
// provider, a repair or a recovered feed can make the source usable later.
func (s *Service) noteFailedHealth(releaseID string) {
	if releaseID == "" {
		return
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failedHealth == nil {
		s.failedHealth = make(map[string]time.Time)
	}
	if len(s.failedHealth) >= 10_000 {
		for id, at := range s.failedHealth {
			if now.Sub(at) >= failedHealthTTL {
				delete(s.failedHealth, id)
			}
		}
		if len(s.failedHealth) >= 10_000 {
			clear(s.failedHealth)
		}
	}
	s.failedHealth[releaseID] = now
}

func (s *Service) recentlyFailedHealth(releaseID string) bool {
	s.mu.RLock()
	at, ok := s.failedHealth[releaseID]
	s.mu.RUnlock()
	return ok && s.now().Sub(at) < failedHealthTTL
}

func init() {
	store.Register("resolver", 2, `
CREATE TABLE resolver_verified (
  release_id TEXT PRIMARY KEY,
  checked_at INTEGER NOT NULL
);`)
}

// checkHealth runs the health checker unless the release passed a check
// within healthTTL. Health checks dominate time-to-playable, and a release
// that was fine an hour ago almost always still is; if it isn't, playback
// fails over mid-stream and MarkBad drops it from the cache.
func (s *Service) checkHealth(ctx context.Context, releaseID string, nzb []byte) (bool, error) {
	ok, _, err := s.checkHealthCached(ctx, releaseID, nzb)
	return ok, err
}

// checkHealthCached is checkHealth that also reports whether the answer
// came from the verified cache rather than a check that just ran.
func (s *Service) checkHealthCached(ctx context.Context, releaseID string, nzb []byte) (ok, cached bool, err error) {
	if s.health == nil {
		return true, true, nil
	}
	if s.recentlyVerified(ctx, releaseID) {
		return true, true, nil
	}
	ok, err = core.CheckReleaseHealth(ctx, s.health, releaseID, nzb)
	if ok && err == nil && ctx.Value(speculativeHealthKey{}) == nil {
		s.markVerified(ctx, releaseID)
	}
	return ok, false, err
}

// markVerified records a passed health check for the verified cache.
func (s *Service) markVerified(ctx context.Context, releaseID string) {
	if s.healthTTL <= 0 || releaseID == "" {
		return
	}
	now := s.now()
	s.mu.Lock()
	s.boundVerifiedLocked(now)
	s.verified[releaseID] = now
	delete(s.verifiedMiss, releaseID)
	s.mu.Unlock()
	if s.db != nil {
		_, _ = s.db.ExecContext(ctx, `INSERT INTO resolver_verified(release_id, checked_at) VALUES (?, ?)
			ON CONFLICT(release_id) DO UPDATE SET checked_at=excluded.checked_at`, releaseID, now.Unix())
	}
}

func (s *Service) recentlyVerified(ctx context.Context, releaseID string) bool {
	if s.healthTTL <= 0 || releaseID == "" {
		return false
	}
	now := s.now()
	s.mu.RLock()
	at, ok := s.verified[releaseID]
	missed, known := s.verifiedMiss[releaseID]
	s.mu.RUnlock()
	if !ok && known && now.Sub(missed) < verifiedMissTTL && !now.Before(missed) {
		return false
	}
	if !ok && s.db != nil {
		var unix int64
		err := s.db.QueryRowContext(ctx, `SELECT checked_at FROM resolver_verified WHERE release_id=?`, releaseID).Scan(&unix)
		switch {
		case err == nil && now.Sub(time.Unix(unix, 0)) < s.healthTTL:
			at, ok = time.Unix(unix, 0), true
			s.mu.Lock()
			s.boundVerifiedLocked(now)
			s.verified[releaseID] = at
			delete(s.verifiedMiss, releaseID)
			s.mu.Unlock()
		case err == nil || errors.Is(err, sql.ErrNoRows):
			// Absent or expired: remember the miss briefly, so ranking
			// many feed releases does not ask the table again per
			// request. markVerified clears it on this node; a check
			// passed on another node is seen once the miss expires.
			s.mu.Lock()
			if s.verifiedMiss == nil {
				s.verifiedMiss = map[string]time.Time{}
			}
			boundMap(s.verifiedMiss, maxVerifiedEntries, func(at time.Time) bool { return now.Sub(at) >= verifiedMissTTL })
			s.verifiedMiss[releaseID] = now
			s.mu.Unlock()
		}
	}
	return ok && now.Sub(at) < s.healthTTL
}

// verifiedMissTTL bounds how long a "not recently verified" answer is
// reused without reading resolver_verified.
const verifiedMissTTL = 2 * time.Minute

// verifiedAmong is recentlyVerified for every release in ids, reading the
// table once per 500 ids instead of once per release: ranking a search with
// hundreds of results must not issue a query for each of them.
func (s *Service) verifiedAmong(ctx context.Context, ids []string) map[string]bool {
	if s.healthTTL <= 0 || len(ids) == 0 {
		return nil
	}
	now := s.now()
	out := make(map[string]bool, len(ids))
	var missing []string
	s.mu.RLock()
	for _, id := range ids {
		if id == "" {
			continue
		}
		if at, ok := s.verified[id]; ok {
			out[id] = now.Sub(at) < s.healthTTL
		} else if missed, known := s.verifiedMiss[id]; known && now.Sub(missed) < verifiedMissTTL && !now.Before(missed) {
			out[id] = false
		} else {
			missing = append(missing, id)
		}
	}
	s.mu.RUnlock()
	if s.db == nil || len(missing) == 0 {
		return out
	}
	slices.Sort(missing)
	missing = slices.Compact(missing)
	found := map[string]time.Time{}
	for start := 0; start < len(missing); start += 500 {
		batch := missing[start:min(start+500, len(missing))]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx, `SELECT release_id, checked_at FROM resolver_verified WHERE release_id IN (`+store.Placeholders(len(batch))+`)`, args...)
		if err != nil {
			continue
		}
		for rows.Next() {
			var id string
			var unix int64
			if rows.Scan(&id, &unix) == nil {
				found[id] = time.Unix(unix, 0)
			}
		}
		rows.Close()
	}
	s.mu.Lock()
	if len(found) > 0 {
		s.boundVerifiedLocked(now)
	}
	for _, id := range missing {
		if at, ok := found[id]; ok && now.Sub(at) < s.healthTTL {
			s.verified[id] = at
			delete(s.verifiedMiss, id)
			continue
		}
		// Absent or expired: remember the miss as recentlyVerified does.
		if s.verifiedMiss == nil {
			s.verifiedMiss = map[string]time.Time{}
		}
		boundMap(s.verifiedMiss, maxVerifiedEntries, func(at time.Time) bool { return now.Sub(at) >= verifiedMissTTL })
		s.verifiedMiss[id] = now
	}
	s.mu.Unlock()
	for id, at := range found {
		out[id] = now.Sub(at) < s.healthTTL
	}
	return out
}

func (s *Service) forgetVerified(ctx context.Context, releaseID string) {
	if s.invalidated != nil {
		s.invalidated(ctx, releaseID)
	}
	s.mu.Lock()
	delete(s.verified, releaseID)
	s.mu.Unlock()
	if s.db != nil {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM resolver_verified WHERE release_id=?`, releaseID)
	}
}

// boundVerifiedLocked keeps the in-memory verified map below its cap; the
// table remains the source of truth, so evicted entries are re-read on
// demand. Callers hold s.mu.
func (s *Service) boundVerifiedLocked(now time.Time) {
	ttl := s.healthTTL
	boundMap(s.verified, maxVerifiedEntries, func(at time.Time) bool { return ttl <= 0 || now.Sub(at) >= ttl })
}
