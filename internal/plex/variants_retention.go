package plex

import (
	"context"
	"database/sql"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/sessions"
	"sync"
	"time"
)

type variantRef struct {
	key       core.RatingKey
	releaseID string
}
type variantIDRegistry struct{ shared *sessions.VariantIDs }

func newVariantIDRegistry(db *sql.DB) *variantIDRegistry { return newVariantIDRegistryAt(db, time.Now) }
func newVariantIDRegistryAt(db *sql.DB, now func() time.Time) *variantIDRegistry {
	return &variantIDRegistry{shared: sessions.NewVariantIDs(db, now)}
}
func variantHash(key core.RatingKey, release string) int64 { return sessions.VariantHash(key, release) }
func (r *variantIDRegistry) idFor(key core.RatingKey, release string) int64 {
	return r.shared.IDFor(key, release)
}
func (r *variantIDRegistry) lookup(id int64) (variantRef, bool) {
	ref, ok := r.shared.Lookup(id)
	return variantRef{ref.Key, ref.ReleaseID}, ok
}
func (r *variantIDRegistry) start(ctx context.Context, wg *sync.WaitGroup) {
	if r != nil {
		r.shared.Start(ctx, wg)
	}
}
func (r *variantIDRegistry) flush(ctx context.Context) error { return r.shared.Flush(ctx) }
func (r *variantIDRegistry) prune(ctx context.Context, ids []int64, refs []variantRef) (int64, error) {
	if r == nil {
		return 0, nil
	}
	shared := make([]sessions.VariantRef, 0, len(refs))
	for _, ref := range refs {
		shared = append(shared, sessions.VariantRef{Key: ref.key, ReleaseID: ref.releaseID})
	}
	return r.shared.Prune(ctx, ids, shared)
}

// liveVariantIDs lists the Media ids and versions every play identity still
// holds: live and admitting sessions, frozen pre-play flows and the version
// lists recently issued to a client.
func (p *playIdentities) liveVariantIDs() ([]int64, []variantRef) {
	if p == nil {
		return nil, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var ids []int64
	var refs []variantRef
	for k, e := range p.m {
		if e.selected != 0 {
			ids = append(ids, e.selected)
		}
		for id := range e.media {
			ids = append(ids, id)
		}
		for _, v := range e.variants {
			if !v.Auto {
				refs = append(refs, variantRef{key: k.key, releaseID: v.Release.ID})
			}
		}
	}
	return ids, refs
}

// Prune removes Play Version ids unseen for 90 days and reports the rows
// removed. Ids a live session or a pinned version list holds are kept.
func (s *Server) Prune(ctx context.Context) (int64, error) {
	if s == nil || s.variantIDs == nil {
		return 0, nil
	}
	ids, refs := s.playIDs.liveVariantIDs()
	return s.variantIDs.prune(ctx, ids, refs)
}
