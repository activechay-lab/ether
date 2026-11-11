package resolver

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// insertNamesakeTitle writes a catalogue row the namesake index reads.
func insertNamesakeTitle(t *testing.T, s *Service, key int64, typ, title string, year int) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO catalog_titles (key,type,title,year) VALUES (?,?,?,?)`, key, typ, title, year); err != nil {
		t.Fatal(err)
	}
}

// TestNamesakeTitlesNormalizedAndBounded is the resolver-side invariant: a
// namesake is a same-media-type title whose normalized title matches and
// whose year is within one year; all other titles are not.
func TestNamesakeTitlesNormalizedAndBounded(t *testing.T) {
	s := New(nil, nil, openTestDB(t), testCfg(), Options{})
	defer s.Close()
	ctx := context.Background()
	// Catalogue keys are genuine RatingKeys, as the catalogue stores them.
	insertNamesakeTitle(t, s, int64(core.MovieKey(1)), "movie", "Runner", 2026)
	insertNamesakeTitle(t, s, int64(core.MovieKey(2)), "movie", "The Runner", 2026) // article-only difference
	insertNamesakeTitle(t, s, int64(core.MovieKey(3)), "movie", "Runner", 1998)     // year beyond +-1
	insertNamesakeTitle(t, s, int64(core.ShowKey(4)), "show", "Runner", 2026)       // different media type
	insertNamesakeTitle(t, s, int64(core.MovieKey(5)), "movie", "Runner 2", 2026)   // different normalized title

	for _, tc := range []struct {
		key  int64
		want bool
	}{
		{1, true},  // "The Runner" 2026 namesakes it
		{2, true},  // "Runner" 2026 namesakes it
		{3, false}, // "Runner" 1998 is too far in year
		{4, false}, // a show is not a movie namesake
	} {
		key := map[int64]core.RatingKey{1: core.MovieKey(1), 2: core.MovieKey(2), 3: core.MovieKey(3), 4: core.ShowKey(4)}[tc.key]
		typ := core.TypeMovie
		if tc.key == 4 {
			typ = core.TypeShow
		}
		title := &core.Title{Key: key, Type: typ,
			Year:  map[int64]int{1: 2026, 2: 2026, 3: 1998, 4: 2026}[tc.key],
			Title: map[int64]string{1: "Runner", 2: "The Runner", 3: "Runner", 4: "Runner"}[tc.key]}
		if got := s.NamesakeTitles(ctx, title); got != tc.want {
			t.Errorf("key %d: namesake = %v, want %v", tc.key, got, tc.want)
		}
	}
}

// TestNamesakeConflictUsesReportedIDs rejects only an explicit identifier
// conflict; missing identifiers are not evidence.
func TestNamesakeConflictUsesReportedIDs(t *testing.T) {
	s := New(nil, nil, openTestDB(t), testCfg(), Options{})
	defer s.Close()
	ctx := context.Background()
	title := &core.Title{Key: core.MovieKey(10), IDs: core.IDs{TMDB: 100, IMDB: "tt0010000"}}
	if !s.NamesakeConflict(ctx, title, core.Release{TitleIDs: core.IDs{TMDB: 200}}) {
		t.Fatal("conflicting tmdb id not detected")
	}
	if s.NamesakeConflict(ctx, title, core.Release{TitleIDs: core.IDs{TMDB: 100}}) {
		t.Fatal("matching tmdb id reported as conflict")
	}
	if s.NamesakeConflict(ctx, title, core.Release{}) {
		t.Fatal("missing identifiers must not conflict")
	}
}
