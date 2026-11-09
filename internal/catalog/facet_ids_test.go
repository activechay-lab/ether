package catalog

import (
	"context"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// A studio or person facet identifier resolves by index once the fold loop
// has named the value, and by hashing the few values not yet named before
// that; a value no title carries resolves nothing.
func TestFacetIDsResolveByIndex(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	c, err := New(db, nil, config.Catalog{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	put := func(key int64, studios ...string) {
		t.Helper()
		title := core.Title{Key: core.MovieKey(key), Type: core.TypeMovie, Title: "T", Released: time.Now().Add(-48 * time.Hour), Studios: studios,
			Cast: []core.Person{{TMDB: 40 + key, Name: "Person"}}}
		if err := c.UpsertTitle(ctx, title, true); err != nil {
			t.Fatal(err)
		}
	}
	put(1, "Acme", "Banner")
	put(2, "Banner", "Échelon")
	check := func(stage string) {
		t.Helper()
		for _, name := range []string{"Acme", "Banner", "Échelon"} {
			if got, ok, err := c.StudioByFacetID(ctx, core.FacetID("studio", name)); err != nil || !ok || got != name {
				t.Errorf("%s: studio %q resolved %q %v %v", stage, name, got, ok, err)
			}
		}
		for _, tmdb := range []int64{41, 42} {
			if got, ok, err := c.PersonByFacetID(ctx, core.FacetID("person", formatInt(tmdb))); err != nil || !ok || got != tmdb {
				t.Errorf("%s: person %d resolved %d %v %v", stage, tmdb, got, ok, err)
			}
		}
		for _, id := range []string{core.FacetID("studio", "Nobody"), core.FacetID("genre", "Acme"), core.FacetID("person", "41x")} {
			if got, ok, err := c.StudioByFacetID(ctx, id); err != nil || ok {
				t.Errorf("%s: %s resolved %q %v", stage, id, got, err)
			}
		}
	}
	check("queued")
	if _, err := c.nameQueuedFacets(ctx); err != nil {
		t.Fatal(err)
	}
	var queued, named int
	if err := db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM catalog_facet_id_queue), (SELECT COUNT(*) FROM catalog_facet_ids)`).Scan(&queued, &named); err != nil || queued != 0 || named != 5 {
		t.Fatalf("after naming: %d queued, %d named, %v", queued, named, err)
	}
	check("named")
	// A whole vocabulary (the migration, a restore) is named alike.
	if err := rebuildFacetIDs(ctx, db); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(ctx, `SELECT id, kind, value FROM catalog_facet_ids`)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for rows.Next() {
		var id, kind, value string
		if err := rows.Scan(&id, &kind, &value); err != nil {
			t.Fatal(err)
		}
		if n++; id != core.FacetID(kind, value) {
			t.Errorf("%s %q named %s", kind, value, id)
		}
	}
	rows.Close()
	if n != 5 {
		t.Errorf("rebuilt %d identifiers", n)
	}
	check("rebuilt")
	// A studio no title carries any more resolves nothing.
	put(1, "Banner")
	if got, ok, err := c.StudioByFacetID(ctx, core.FacetID("studio", "Acme")); err != nil || ok {
		t.Errorf("a vanished studio resolved %q %v", got, err)
	}
}

func formatInt(n int64) string { return strconv.FormatInt(n, 10) }
