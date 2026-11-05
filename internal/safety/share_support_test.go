package safety

import (
	"github.com/braedonsaunders/ether/internal/sharefilter"
)

// FormatShareFilter turns a plex.tv filter spec into owner-readable parts.
func FormatShareFilter(spec string) []ShareFilterPart {
	parts := sharefilter.Format(spec)
	out := make([]ShareFilterPart, len(parts))
	for i, p := range parts {
		out[i] = ShareFilterPart{Kind: p.Kind, Values: p.Values}
	}
	return out
}
