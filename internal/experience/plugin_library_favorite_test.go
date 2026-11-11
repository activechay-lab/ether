package experience

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// Plugin library titles are never favourites in this build. A Jellyfin client
// asking `Filters=IsFavorite` on a plugin library must get an empty page, not a
// 503 from the favourites policy refusing the plugin keys.
func TestFilterPluginTitlesFavoriteIsEmpty(t *testing.T) {
	s := &Service{}
	items := []core.Title{{Key: core.PluginKey(1)}, {Key: core.PluginKey(2)}}
	yes := true
	out, err := s.filterPluginTitles(context.Background(), &core.User{ID: 1}, items, frontend.ItemQuery{Favorite: &yes})
	if err != nil {
		t.Fatalf("favorite filter returned error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("favorite filter kept %d plugin titles, want 0", len(out))
	}
	no := false
	out, err = s.filterPluginTitles(context.Background(), &core.User{ID: 1}, items, frontend.ItemQuery{Favorite: &no})
	if err != nil {
		t.Fatalf("non-favorite filter returned error: %v", err)
	}
	if len(out) != len(items) {
		t.Fatalf("non-favorite filter kept %d plugin titles, want %d", len(out), len(items))
	}
}
