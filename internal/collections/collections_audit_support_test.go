package collections

import (
	"github.com/braedonsaunders/ether/internal/core"
)

// WithExternalBases points external list fetches at other origins (tests).
func WithExternalBases(b ExternalBases) Option { return func(s *Service) { s.opts.bases = &b } }

// BuiltinKey returns the rating key of a built-in collection by slug
// (e.g. "trending-week-movie"), or 0.
func BuiltinKey(slug string) core.RatingKey {
	for _, b := range builtins {
		if b.slug == slug {
			return core.SmartKey(b.id)
		}
	}
	return 0
}
