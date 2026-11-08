package catalog

import "github.com/braedonsaunders/ether/internal/core"

// mergeTailHits merges long-tail hits into the library's search results.
// Placeholder until the search-quality work (K) replaces this file: tail
// hits keep their index score, and a key the library already answered is
// not added twice.
func mergeTailHits(lib []core.Title, tail []core.Title, tailScore map[core.RatingKey]float64, limit int) []core.Title {
	_ = limit
	seen := make(map[core.RatingKey]bool, len(lib))
	for _, t := range lib {
		seen[t.Key] = true
	}
	out := lib
	for _, t := range tail {
		if seen[t.Key] {
			continue
		}
		seen[t.Key] = true
		t.SearchScore = tailScore[t.Key]
		out = append(out, t)
	}
	return out
}
