package plex

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

const (
	shelfPendingLimit = 256 // plus at most one equally bounded active batch
	shelfCoalesce     = 20 * time.Millisecond
	shelfWriteTimeout = time.Second
)

type shelfWrite struct {
	generation, scope, path string
	keys                    []frontend.RatingKey
	expires                 int64
}
type shelfBatchKey struct{}
type shelfBatch struct{ records map[string]shelfWrite }
type shelfPersistence struct {
	mu      sync.Mutex
	pending map[string]shelfWrite
	active  map[string]bool
	wake    chan struct{}
	commits atomic.Uint64
	rows    atomic.Uint64
	dropped atomic.Uint64
	swept   time.Time // owned by the single writer
}

func (s *Server) stageShelf(ctx context.Context, record shelfWrite) {
	if batch, ok := ctx.Value(shelfBatchKey{}).(*shelfBatch); ok {
		if batch.records == nil {
			batch.records = map[string]shelfWrite{}
		}
		batch.records[record.generation] = record
		return
	}
	s.enqueueShelves(map[string]shelfWrite{record.generation: record})
}

// One screen is offered atomically. Queue admission never waits for SQL or
// capacity: coalesce duplicate identities and drop a new batch on overload.
func (s *Server) enqueueShelves(records map[string]shelfWrite) bool {
	w := s.shelfWriter
	if len(records) == 0 || s.d.DB == nil || w == nil || s.ctx.Err() != nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	added := 0
	for key := range records {
		if _, ok := w.pending[key]; !ok && !w.active[key] {
			added++
		}
	}
	if len(w.pending)+added > shelfPendingLimit {
		w.dropped.Add(uint64(added))
		return false
	}
	for key, record := range records {
		if w.active[key] {
			continue
		}
		if prior, ok := w.pending[key]; !ok || prior.expires < record.expires {
			w.pending[key] = record
		}
	}
	if len(w.pending) > 0 {
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
	return true
}

func (s *Server) writeShelves() {
	defer s.wg.Done()
	w := s.shelfWriter
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-w.wake:
		}
		timer := time.NewTimer(shelfCoalesce)
		select {
		case <-s.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		w.mu.Lock()
		batch := w.pending
		w.pending = map[string]shelfWrite{}
		for key := range batch {
			w.active[key] = true
		}
		select {
		case <-w.wake:
		default:
		}
		w.mu.Unlock()
		if len(batch) == 0 {
			continue
		}
		ctx, cancel := context.WithTimeout(s.ctx, shelfWriteTimeout)
		err := s.persistShelfBatch(ctx, batch)
		cancel()
		if err != nil && s.ctx.Err() == nil {
			s.log.Warn("persist shelf batch", "generations", len(batch), "err", err)
		}
		w.mu.Lock()
		for key := range batch {
			delete(w.active, key)
		}
		w.mu.Unlock()
	}
}

func (s *Server) noteShelfPersistence(generation string, expires int64, attempt time.Time) {
	s.shelfMu.Lock()
	if g, ok := s.shelfGenerations[generation]; ok {
		g.persisted = true
		g.expires = expires
		g.lastPersist = attempt
		s.shelfGenerations[generation] = g
	}
	s.shelfMu.Unlock()
}

// Read first: after a restart or eviction, an unknown local generation can
// already exist in the shared store. Such a batch must not acquire the writer.
func (s *Server) persistShelfBatch(ctx context.Context, batch map[string]shelfWrite) error {
	args := make([]any, 0, len(batch))
	for key := range batch {
		args = append(args, key)
	}
	existing := map[string]int64{}
	rows, err := s.d.DB.QueryContext(ctx, `SELECT generation,expires_at FROM plex_shelf_generations WHERE generation IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`)`, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key string
		var expires int64
		if err := rows.Scan(&key, &expires); err != nil {
			rows.Close()
			return err
		}
		existing[key] = expires
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	now := s.now()
	cutoff := now.Add(24 * time.Hour).Unix()
	work := make([]shelfWrite, 0, len(batch))
	for key, record := range batch {
		if expires, ok := existing[key]; ok {
			s.noteShelfPersistence(key, expires, now)
			if expires >= cutoff {
				continue
			}
		}
		work = append(work, record)
	}
	if len(work) == 0 {
		return nil
	}
	tx, err := s.d.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var changed uint64
	for _, record := range work {
		var n int64
		if _, ok := existing[record.generation]; ok {
			result, err := tx.ExecContext(ctx, `UPDATE plex_shelf_generations SET expires_at=? WHERE generation=? AND expires_at<?`, record.expires, record.generation, cutoff)
			if err != nil {
				return err
			}
			n, err = result.RowsAffected()
			if err != nil {
				return err
			}
		} else {
			data, err := json.Marshal(record.keys)
			if err != nil {
				return err
			}
			result, err := tx.ExecContext(ctx, `INSERT INTO plex_shelf_generations(generation,scope,path,keys_json,expires_at) VALUES(?,?,?,?,?) ON CONFLICT(generation) DO NOTHING`, record.generation, record.scope, record.path, string(data), record.expires)
			if err != nil {
				return err
			}
			n, err = result.RowsAffected()
			if err != nil {
				return err
			}
		}
		changed += uint64(n)
	}
	sweep := s.shelfWriter.swept.IsZero() || now.Sub(s.shelfWriter.swept) >= time.Hour
	if sweep {
		if _, err := tx.ExecContext(ctx, `DELETE FROM plex_shelf_generations WHERE expires_at<?`, now.Unix()); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.shelfWriter.commits.Add(1)
	s.shelfWriter.rows.Add(changed)
	if sweep {
		s.shelfWriter.swept = now
	}
	for _, record := range work {
		s.noteShelfPersistence(record.generation, record.expires, now)
	}
	return nil
}
