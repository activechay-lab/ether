package availability

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"time"
)

type visibilityEntry struct {
	at      time.Time
	visible bool
}

// A short local memo keeps repeated Plex artwork/list requests inexpensive.
// New observations and invalidations clear it immediately. Other cluster nodes
// see changes within fifteen seconds, without a provider request.
func (s *Service) FilterDiscovery(ctx context.Context, items []core.Title) []core.Title {
	if len(items) == 0 {
		return items
	}
	snap := s.snapshotFor(ctx)
	for _, t := range items {
		if evidenceWindow(t, snap.now, snap.availability) {
			return s.filterDiscoverySnapshot(ctx, items, snap)
		}
	}
	out := make([]core.Title, len(items))
	copy(out, items)
	return out
}

func (s *Service) filterDiscoverySnapshot(ctx context.Context, items []core.Title, input discoverySnapshot) []core.Title {
	now := input.now
	keys := make(map[core.RatingKey]string)
	allowed := map[core.RatingKey]bool{}
	var pending []core.Title
	policy := fmt.Sprintf("%s:%+v", input.policy, input.availability)
	feeds := []byte(fmt.Sprintf("%+v", input.feeds))
	sets := s.answeredSets(input)
	s.visibilityMu.Lock()
	revision := s.visibilityRevision
	for _, t := range items {
		if input.upcomingMovie(t) || !evidenceWindow(t, now, input.availability) {
			allowed[t.Key] = true
			continue
		}
		// A title the last hidden set judged reads that answer, so a hub
		// shows exactly what the library pages exclude.
		if h := sets[t.Type]; h != nil && h.covered[t.Key] {
			allowed[t.Key] = !h.hidden[t.Key]
			continue
		}
		if _, seen := keys[t.Key]; seen {
			continue
		}
		raw, _ := json.Marshal(t)
		key := fmt.Sprintf("%s:%x:%x", policy, sha256.Sum256(feeds), sha256.Sum256(raw))
		keys[t.Key] = key
		if e, ok := s.visibilityCache[key]; ok && now.Sub(e.at) < 15*time.Second && !now.Before(e.at) {
			allowed[t.Key] = e.visible
		} else {
			pending = append(pending, t)
		}
	}
	s.visibilityMu.Unlock()
	if len(pending) > 0 {
		for _, t := range s.filterDiscovery(ctx, pending, input) {
			allowed[t.Key] = true
		}
		s.visibilityMu.Lock()
		if revision == s.visibilityRevision {
			if s.visibilityCache == nil || len(s.visibilityCache)+len(pending) > 8192 {
				s.visibilityCache = map[string]visibilityEntry{}
			}
			for _, t := range pending {
				s.visibilityCache[keys[t.Key]] = visibilityEntry{now, allowed[t.Key]}
			}
		}
		s.visibilityMu.Unlock()
	}
	out := make([]core.Title, 0, len(items))
	for _, t := range items {
		if allowed[t.Key] {
			out = append(out, t)
		}
	}
	return out
}
func (s *Service) clearVisibility() {
	s.visibilityMu.Lock()
	s.visibilityRevision++
	s.visibilityCache = nil
	s.visibilityMu.Unlock()
}

type currentPolicyKey struct{}

// FilterDiscoveryCurrent bypasses node-local visibility and resolver memos for
// one bounded policy preflight batch. It performs no provider lookup.
func (s *Service) FilterDiscoveryCurrent(ctx context.Context, items []core.Title) []core.Title {
	return s.filterDiscovery(context.WithValue(ctx, currentPolicyKey{}, true), items, s.discoverySnapshot())
}

// answeredSets returns the hidden sets that exist and answer for input's
// settings, refreshing stale ones as a library page would. It never
// creates a set: a row before the first refresh judges its own page.
func (s *Service) answeredSets(input discoverySnapshot) map[core.MediaType]*hiddenSet {
	out := map[core.MediaType]*hiddenSet{}
	for kind, typ := range map[string]core.MediaType{hiddenMovies: core.TypeMovie, hiddenShows: core.TypeShow} {
		if kind == hiddenMovies && movieImmediate(input.availability) {
			continue
		}
		if kind == hiddenShows && input.availability.ShowMode == config.ModeImmediate {
			continue
		}
		s.visibilityMu.Lock()
		_, exists := s.hiddenSets[kind]
		s.visibilityMu.Unlock()
		if !exists {
			continue
		}
		if h := s.currentHidden(kind, input); h != nil && h.key == hiddenKey(kind, input) {
			out[typ] = h
		}
	}
	return out
}

// JudgedOnFacts returns, for one request, whether FilterDiscovery judges a
// title on its facts: one inside the evidence window, whose releases are
// matched against its names, genres, keywords, countries and directors. A
// caller holding partial titles passes those in full.
func (s *Service) JudgedOnFacts(ctx context.Context) func(core.Title) bool {
	snap := s.snapshotFor(ctx)
	return func(t core.Title) bool {
		return !snap.upcomingMovie(t) && evidenceWindow(t, snap.now, snap.availability)
	}
}
