package experience

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
)

func TestXtreamExportUsesDiscoveryAndViewerPolicy(t *testing.T) {
	viewer := &core.User{ID: 1, AllowedLibraries: []core.LibraryID{1}}
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

// Viewers with different restrictions never share an export stamp, and a
// policy that judges titles on their facts says so.
func TestExportPolicyStampsRestrictions(t *testing.T) {
	users := map[int64]*core.User{1: {ID: 1, IsOwner: true}, 2: {ID: 2, MaxContentRating: "PG"}, 3: {ID: 3}, 4: {ID: 4}}
	s := &Service{d: ServiceDeps{Active: func(_ context.Context, id int64) (*core.User, error) { return users[id], nil }, Libraries: []Library{{ID: 1, Type: core.TypeMovie}},
		Visibility: Visibility{SectionFor: func(core.MediaType) (int, bool) { return 1, true }},
		Browse:     func(context.Context, core.BrowseQuery) (core.Page, error) { return core.Page{}, nil },
		ViewerRules: func(u *core.User) (int64, bool) {
			if u.ID == 4 {
				return 7, true
			}
			return 0, false
		}}}
	seen := map[string]int64{}
	for id, judged := range map[int64]bool{1: false, 2: true, 3: false, 4: true} {
		p, err := s.ExportPolicy(context.Background(), users[id], core.Device{}, "1")
		if err != nil || p.TitleJudged != judged || p.Stamp == "" {
			t.Fatalf("viewer %d: %+v %v", id, p, err)
		}
		if other, ok := seen[p.Stamp]; ok {
			t.Fatalf("viewers %d and %d share a stamp", id, other)
		}
		seen[p.Stamp] = id
	}
	again, _ := s.ExportPolicy(context.Background(), users[2], core.Device{}, "1")
	users[2].MaxContentRating = "R"
	if changed, _ := s.ExportPolicy(context.Background(), users[2], core.Device{}, "1"); changed.Stamp == again.Stamp {
		t.Fatal("a rating change kept the stamp")
	}
}
