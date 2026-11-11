package libraries

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestSetRulesAndMigrateConfigRules(t *testing.T) {
	s, _ := openService(t)
	ctx := context.Background()

	legacy := []core.LibraryExclusion{
		{Name: "No Horror Movies", MediaType: "movie", Match: "all", Rules: []core.CollectionRule{{Field: "genre", Op: "is", Value: json.RawMessage(`"Horror"`)}}},
		{Name: "No Reality TV", MediaType: "show", Match: "all", Rules: []core.CollectionRule{{Field: "genre", Op: "is", Value: json.RawMessage(`"Reality"`)}}},
		{Name: "English Only", MediaType: "mixed", Match: "all", Rules: []core.CollectionRule{{Field: "language", Op: "isNot", Value: json.RawMessage(`"en"`)}}},
	}
	if err := s.MigrateConfigRules(ctx, legacy, "hide"); err != nil {
		t.Fatal(err)
	}
	movies, err := s.Get(ctx, core.LibraryMovies)
	if err != nil {
		t.Fatal(err)
	}
	shows, err := s.Get(ctx, core.LibraryShows)
	if err != nil {
		t.Fatal(err)
	}
	if movies.VisibilityDefault != "hide" || len(movies.Rules) != 2 || movies.Rules[0].Name != "No Horror Movies" || movies.Rules[1].Name != "English Only" || movies.Rules[1].MediaType != "movie" {
		t.Fatalf("migrated movies = %+v", movies)
	}
	if shows.VisibilityDefault != "hide" || len(shows.Rules) != 2 || shows.Rules[0].Name != "No Reality TV" || shows.Rules[1].Name != "English Only" || shows.Rules[1].MediaType != "show" {
		t.Fatalf("migrated shows = %+v", shows)
	}

	// Owner edits win over a subsequent startup call (one-time migration).
	if err := s.SetRules(ctx, core.LibraryMovies, []core.LibraryExclusion{{Name: "Custom Movie Rule", MediaType: "movie", Match: "all", Rules: []core.CollectionRule{{Field: "country", Op: "is", Value: json.RawMessage(`"JP"`)}}}}, "show"); err != nil {
		t.Fatal(err)
	}
	if err := s.MigrateConfigRules(ctx, legacy, "hide"); err != nil {
		t.Fatal(err)
	}
	moviesAfter, err := s.Get(ctx, core.LibraryMovies)
	if err != nil {
		t.Fatal(err)
	}
	if moviesAfter.VisibilityDefault != "" || len(moviesAfter.Rules) != 1 || moviesAfter.Rules[0].Name != "Custom Movie Rule" {
		t.Fatalf("migration overwrote owner edits: %+v", moviesAfter)
	}
}
