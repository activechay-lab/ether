package playback

import (
	"context"
	"math"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Namesake runtime gate. Two catalogue movies can share a normalized title
// and a near year ("Runner" and "The Runner", both 2026); release names then
// cannot tell them apart, so an indexer may serve the other film's file.
// When the catalogue holds a namesake the request is ambiguous, and a probed
// duration that contradicts the catalogue runtime rejects the candidate like
// any other bad release, failing over to the next. Unambiguous titles keep
// the old behaviour, so extended cuts of an only-title are unaffected.

// namesakeRuntimeTolerance is the allowed difference between a probed file
// and the catalogue runtime: the larger of these two bounds.
const (
	namesakeRuntimeSlack      = 8 * time.Minute
	namesakeRuntimeSlackRatio = 0.12
)

// NamesakeAware, implemented by the resolver, indexes normalized titles for
// bounded namesake lookups and answers whether a release's reported external
// identifiers name a different catalogue title.
type NamesakeAware interface {
	// NamesakeTitles reports a same-media-type catalogue title whose
	// normalized title matches t's and whose year is within one year of
	// t's (never t itself).
	NamesakeTitles(ctx context.Context, t *core.Title) bool
	// NamesakeConflict reports a release whose reported identifiers
	// explicitly name a different catalogue title than t.
	NamesakeConflict(ctx context.Context, t *core.Title, r core.Release) bool
}

func (m *Manager) namesakes(ctx context.Context, t *core.Title) (NamesakeAware, bool) {
	if t == nil || m.deps.Resolver == nil || t.KindLevel != "" {
		return nil, false
	}
	n, ok := m.deps.Resolver.(NamesakeAware)
	return n, ok
}

// namesakeRequest reports whether t is one of two namesake catalogue titles.
func (m *Manager) namesakeRequest(ctx context.Context, t *core.Title) bool {
	n, ok := m.namesakes(ctx, t)
	return ok && n.NamesakeTitles(ctx, t)
}

// namesakeTitleConflict reports whether r's reported identifiers name a
// different catalogue title than t.
func (m *Manager) namesakeTitleConflict(ctx context.Context, t *core.Title, r core.Release) bool {
	n, ok := m.namesakes(ctx, t)
	return ok && n.NamesakeConflict(ctx, t, r)
}

// namesakeDurationMismatch reports whether a probed duration contradicts the
// catalogue runtime by more than the tolerance.
func namesakeDurationMismatch(runtime, probed time.Duration) bool {
	if runtime <= 0 || probed <= 0 {
		return false
	}
	allow := math.Max(namesakeRuntimeSlack.Seconds(), namesakeRuntimeSlackRatio*runtime.Seconds())
	return math.Abs(probed.Seconds()-runtime.Seconds()) > allow
}

// releaseNamesakeRejected gates a candidate for an ambiguous (namesake)
// request: a release whose reported identifiers name the other title is
// rejected before opening. Unambiguous titles are never gated.
func (m *Manager) releaseNamesakeRejected(ctx context.Context, s *session, r core.Release) bool {
	if s.title == nil || !m.namesakeRequest(ctx, s.title) {
		return false
	}
	return m.namesakeTitleConflict(ctx, s.title, r)
}
