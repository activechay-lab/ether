package catalog

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// A restricted viewer's rules never let through a title they cannot judge.
// TMDB search hits carry no certification or countries, and a stored row
// without details may lack them too: a rule like "hide rated R and above"
// answers "no match" on an unknown rating, so such titles are dropped for a
// custom (kid) rule set or a rating-capped viewer, judged on their details
// once the catalogue has them, and unchanged for the owner and unrestricted
// profiles.
func TestRestrictedViewerRulesFailClosedOnUnknownFields(t *testing.T) {
	db := store.OpenTest(t)
	global := []core.LibraryExclusion{{Name: "Horror", MediaType: "mixed", Match: "all", Rules: []core.CollectionRule{{Field: "genre", Op: "in", Value: json.RawMessage(`["Horror"]`)}}}}
	c, err := New(db, nil, config.Catalog{Exclusions: global}, nil)
	if err != nil {
		t.Fatal(err)
	}
	kidRules := ViewerRules{Mode: "custom", VisibilityDefault: "show", Exclusions: []core.LibraryExclusion{
		{Name: "R and above", MediaType: "mixed", Match: "all", Rules: []core.CollectionRule{{Field: "contentRating", Op: "gte", Value: json.RawMessage(`"R"`)}}},
		{Name: "Country", MediaType: "mixed", Match: "all", Rules: []core.CollectionRule{{Field: "country", Op: "in", Value: json.RawMessage(`["IN"]`)}}},
		{Name: "Horror", MediaType: "mixed", Match: "all", Rules: []core.CollectionRule{{Field: "genre", Op: "in", Value: json.RawMessage(`["Horror"]`)}}},
	}}
	kid, capped, adult := &core.User{ID: 7}, &core.User{ID: 8, MaxContentRating: "PG"}, &core.User{ID: 9}
	if err := c.PutViewerRules(t.Context(), kid.ID, kidRules); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-48 * time.Hour)
	movie := func(key int64, rating string, countries, genres []string) core.Title {
		return core.Title{Key: core.MovieKey(key), Type: core.TypeMovie, Title: "t", Released: past, ContentRating: rating, Countries: countries, Genres: genres}
	}
	bare := movie(1, "", nil, nil)                                  // TMDB hit: nothing known
	clean := movie(2, "PG", []string{"US"}, []string{"Comedy"})     // fully known, allowed
	rated := movie(3, "R", []string{"US"}, []string{"Comedy"})      // known, blocked by rating
	undetailed := movie(4, "", nil, nil)                            // stored row, no details
	detailed := movie(5, "", []string{"US"}, []string{"Animation"}) // details say "not rated"
	for _, s := range []struct {
		title   core.Title
		details bool
	}{{undetailed, false}, {detailed, true}} {
		if err := c.upsert(t.Context(), s.title, upsertOpts{browsable: true}); err != nil {
			t.Fatal(err)
		}
		if s.details {
			if _, err := db.Exec(`UPDATE catalog_titles SET detail=1, detail_at=? WHERE key=?`, time.Now().Unix(), int64(s.title.Key)); err != nil {
				t.Fatal(err)
			}
		}
	}
	hits := []core.Title{bare, clean, rated, {Key: undetailed.Key, Type: core.TypeMovie, Title: "t", Released: past}, {Key: detailed.Key, Type: core.TypeMovie, Title: "t", Released: past}}
	visible := func(u *core.User, search bool) map[core.RatingKey]bool {
		ctx := t.Context()
		if u != nil {
			ctx = core.WithCatalogViewer(ctx, u)
		}
		if search {
			ctx = core.WithExplicitSearch(ctx)
		}
		out := map[core.RatingKey]bool{}
		for _, ti := range c.FilterVisibility(ctx, hits) {
			out[ti.Key] = true
		}
		return out
	}
	// Library SQL agrees: the kid's browse drops the undetailed row.
	for _, tc := range []struct {
		u    *core.User
		want int
	}{{kid, 1}, {adult, 2}} {
		page, err := c.Browse(core.WithCatalogViewer(t.Context(), tc.u), core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}, Limit: 10})
		if err != nil || page.Total != tc.want || len(page.Items) != tc.want {
			t.Fatalf("browse for %d: total %d items %d err %v, want %d", tc.u.ID, page.Total, len(page.Items), err, tc.want)
		}
	}
	// Single-title checks (safety.Allowed on every surface: detail, play,
	// Stremio, Silo, MCP) apply the same rule.
	for _, tc := range []struct {
		u    *core.User
		t    core.Title
		want bool
	}{{kid, bare, false}, {kid, undetailed, false}, {kid, detailed, true}, {kid, clean, true}, {capped, bare, false}, {adult, bare, true}, {adult, undetailed, true}} {
		ti := tc.t
		if got := c.VisibleForViewer(tc.u, &ti); got != tc.want {
			t.Fatalf("VisibleForViewer(%d, %d) = %v, want %v", tc.u.ID, ti.Key, got, tc.want)
		}
		if got := c.DiscoveryVisibleForViewer(tc.u, &ti); got != tc.want {
			t.Fatalf("DiscoveryVisibleForViewer(%d, %d) = %v, want %v", tc.u.ID, ti.Key, got, tc.want)
		}
	}
	for _, search := range []bool{false, true} {
		for _, tc := range []struct {
			name string
			u    *core.User
			want []int64
		}{
			{"kid rule set", kid, []int64{2, 5}},
			{"rating-capped, inherited rules", capped, []int64{2, 3, 5}},
			{"unrestricted profile", adult, []int64{1, 2, 3, 4, 5}},
			{"owner", nil, []int64{1, 2, 3, 4, 5}},
		} {
			got := visible(tc.u, search)
			if len(got) != len(tc.want) {
				t.Fatalf("%s (search=%v): visible %v, want %v", tc.name, search, got, tc.want)
			}
			for _, k := range tc.want {
				if !got[core.MovieKey(k)] {
					t.Fatalf("%s (search=%v): visible %v, want %v", tc.name, search, got, tc.want)
				}
			}
		}
	}
}
