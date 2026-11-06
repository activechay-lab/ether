package experience

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
)

func TestXtreamExportUsesDiscoveryAndViewerPolicy(t *testing.T) {
	viewer := &core.User{ID: 1, AllowedSections: []int{1}}
	titles := []core.Title{{Key: core.MovieKey(1), Type: core.TypeMovie, Adult: true}, {Key: core.MovieKey(2), Type: core.TypeMovie, Title: "Unavailable new release"}, {Key: core.MovieKey(3), Type: core.TypeMovie, Title: "Playable"}}
	calls := 0
	s := &Service{d: ServiceDeps{Active: func(context.Context, int64) (*core.User, error) { return viewer, nil }, Libraries: []Library{{ID: 1, Type: core.TypeMovie}}, Visibility: Visibility{SectionFor: func(core.MediaType) (int, bool) { return 1, true }, FilterAvailable: func(_ context.Context, ts []core.Title) []core.Title {
		out := []core.Title{}
		for _, t := range ts {
			if t.Key != core.MovieKey(2) {
				out = append(out, t)
			}
		}
		return out
	}}, Browse: func(_ context.Context, q core.BrowseQuery) (core.Page, error) {
		calls++
		if q.Offset != 0 || q.Sort != core.SortKey || !q.NoTotal || q.Limit != 250 {
			t.Fatal("bad export query")
		}
		return core.Page{Items: titles}, nil
	}}}
	p, err := s.ExportLibraryPage(context.Background(), viewer, core.Device{}, "1", 0, 250)
	if err != nil || len(p.Items) != 1 || p.Items[0].Key != core.MovieKey(3) || p.Next != core.MovieKey(3) || !p.Done {
		t.Fatalf("export %+v %v", p, err)
	}
	_, err = s.ExportLibraryPage(context.Background(), viewer, core.Device{}, "2", 0, 250)
	if err == nil || calls != 1 {
		t.Fatal("unscoped library browse")
	}
}
