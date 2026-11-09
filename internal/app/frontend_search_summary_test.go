package app

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type searchSummaryState struct{ core.State }

func TestSurfaceSearchSummaryProjection(t *testing.T) {
	for _, protocol := range []core.Protocol{"stremio", "jellyfin", "plex"} {
		t.Run(string(protocol), func(t *testing.T) {
			viewer := &core.User{ID: 1, IsOwner: true}
			get := func(context.Context, core.RatingKey) (*core.Title, error) { return nil, core.ErrNotFound }
			calls := 0
			svc, err := experience.NewService(experience.ServiceDeps{
				Active:    func(context.Context, int64) (*core.User, error) { return viewer, nil },
				Get:       func(ctx context.Context, _ *core.User, key core.RatingKey) (*core.Title, error) { return get(ctx, key) },
				Children:  func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) { return nil, nil },
				Libraries: []experience.Library{{ID: 1, Type: core.TypeMovie}, {ID: 2, Type: core.TypeShow}},
				Protocol:  protocol, State: searchSummaryState{}, Rows: &experience.RowsService{},
				Visibility: experience.Visibility{SectionFor: func(core.MediaType) (int, bool) { return 1, true }, GetTitle: get},
				Search: experience.Search{
					Unrestricted: func(*core.User) bool { return true },
					Catalog: func(ctx context.Context, query string, types []core.MediaType, limit int) ([]core.Title, error) {
						calls++
						if core.SearchSummaryFacts(ctx) != (protocol == "stremio") {
							t.Fatal("incorrect protocol projection")
						}
						if query != "star" || limit != 200 || len(types) != 1 || types[0] != core.TypeMovie {
							t.Fatalf("candidate request changed: %q %v %d", query, types, limit)
						}
						return []core.Title{
							{Key: core.MovieKey(1), Type: core.TypeMovie, Title: "Star", Genres: []string{"Drama"}},
							{Key: core.MovieKey(2), Type: core.TypeMovie, Title: "Star Comedy", Genres: []string{"Comedy"}},
							{Key: core.MovieKey(3), Type: core.TypeMovie, Title: "Star Hidden", Genres: []string{"Drama"}, Adult: true},
						}, nil
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			surface := &surfaceExperience{Service: svc, protocol: protocol}
			page, err := surface.Search(context.Background(), viewer, core.Device{}, "star", frontend.ItemQuery{Limit: 100, Types: []core.MediaType{core.TypeMovie}, Filter: core.Filter{Genres: []string{"Drama"}}})
			if err != nil || calls != 1 || len(page.Page.Items) != 1 || page.Page.Items[0].Key != core.MovieKey(1) {
				t.Fatalf("filtered summary page: %+v, calls=%d, err=%v", page, calls, err)
			}
		})
	}
}
