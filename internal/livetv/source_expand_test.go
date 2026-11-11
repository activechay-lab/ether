package livetv

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// A show source with a known-unavailable episode baked in would otherwise
// sit in a repeating channel forever, hitting the live broadcast's runtime
// fallback on every single cycle. The resolver should drop it once here.
func TestExpandProgramSourceDropsKnownUnavailableEpisodes(t *testing.T) {
	showKey := core.RatingKey(500)
	unavailable := core.EpisodeKey(int64(showKey), 1, 2)
	s := testService(t, Options{
		Catalog: &showcaseTestCatalog{},
		Availability: availFunc(func(key core.RatingKey) (bool, bool) {
			if key == unavailable {
				return true, false // known, not available
			}
			return false, false // unknown either way - never filtered
		}),
	})

	items, err := s.expandProgramSource(context.Background(), &ProgramSource{Kind: "show", ShowKey: showKey}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 remaining episodes, got %d: %+v", len(items), items)
	}
	for _, it := range items {
		if it.Key == unavailable {
			t.Fatalf("known-unavailable episode %v was not dropped: %+v", unavailable, items)
		}
	}
}
