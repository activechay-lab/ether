package experience

import (
	"context"
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestSearchCandidateTypesRespectLibraryAccess(t *testing.T) {
	for _, tc := range []struct {
		name            string
		requested, want []core.MediaType
		unserved        []string
		excluded        []core.MediaType
		sections        []core.LibraryID
	}{
		{name: "unsupported", unserved: []string{"musicalbum"}},
		{name: "excluded", requested: []core.MediaType{core.TypeMovie}, excluded: []core.MediaType{core.TypeMovie}},
		{name: "movie", requested: []core.MediaType{core.TypeMovie}, want: []core.MediaType{core.TypeMovie}},
		{name: "episode", requested: []core.MediaType{core.TypeEpisode}, want: []core.MediaType{core.TypeEpisode}},
		{name: "all", want: []core.MediaType{core.TypeMovie, core.TypeShow, core.TypeSeason, core.TypeEpisode}},
		{name: "denied movie", requested: []core.MediaType{core.TypeMovie}, sections: []core.LibraryID{2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viewer := &core.User{ID: 1, AllowedLibraries: tc.sections}
			var got []core.MediaType
			s := &Service{d: ServiceDeps{
				Active:    func(context.Context, int64) (*core.User, error) { return viewer, nil },
				Libraries: []Library{{ID: 1, Type: core.TypeMovie}, {ID: 2, Type: core.TypeShow}},
				Search: Search{Catalog: func(_ context.Context, _ string, types []core.MediaType, _ int) ([]core.Title, error) {
					got = slices.Clone(types)
					return nil, nil
				}},
			}}
			_, err := s.Search(t.Context(), viewer, core.Device{}, "star", frontend.ItemQuery{Types: tc.requested, UnservedTypes: tc.unserved, ExcludedTypes: tc.excluded})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("catalogue types = %v, want %v", got, tc.want)
			}
		})
	}
}
