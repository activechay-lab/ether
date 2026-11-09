// Package homefilter shares bounded discovery suitability checks during one
// multi-request Home load. Viewer authorization remains a separate check.
package homefilter

import (
	"context"
	"sync"

	"github.com/braedonsaunders/ether/internal/core"
)

type contextKey struct{}

type Snapshot struct {
	mu        sync.Mutex
	decisions map[core.RatingKey]bool
}

func With(ctx context.Context, snapshot *Snapshot) context.Context {
	return context.WithValue(ctx, contextKey{}, snapshot)
}

// Allowed preserves the producer's first occurrence for each key, just as the
// catalogue filter does. Oversized pools are processed but never retained.
func Allowed(ctx context.Context, titles []core.Title, filter func(context.Context, []core.Title) []core.Title) map[core.RatingKey]bool {
	s, _ := ctx.Value(contextKey{}).(*Snapshot)
	if s == nil {
		out := map[core.RatingKey]bool{}
		for _, t := range filter(ctx, titles) {
			out[t.Key] = true
		}
		return out
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.decisions == nil {
		s.decisions = map[core.RatingKey]bool{}
	}
	seen := map[core.RatingKey]bool{}
	var missing []core.Title
	out := map[core.RatingKey]bool{}
	for _, t := range titles {
		if seen[t.Key] {
			continue
		}
		seen[t.Key] = true
		if v, ok := s.decisions[t.Key]; ok {
			if v {
				out[t.Key] = true
			}
		} else {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		allowed := map[core.RatingKey]bool{}
		for _, t := range filter(ctx, missing) {
			allowed[t.Key] = true
		}
		for _, t := range missing {
			v := allowed[t.Key]
			if v {
				out[t.Key] = true
			}
			if ctx.Err() == nil && len(s.decisions) < 2048 {
				s.decisions[t.Key] = v
			}
		}
	}
	return out
}
