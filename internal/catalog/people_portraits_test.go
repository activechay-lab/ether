package catalog_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestCachedPortraitTitlesWorkBound(t *testing.T) {
	db := store.OpenTest(t)
	c, err := catalog.New(db, nil, config.Catalog{}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("portrait work-count backend: %s", store.DialectOf(db))
	ctx := context.Background()
	now := time.Now().UTC()
	key := core.ShowKey(812)
	person := core.Person{TMDB: 42, Name: "Requested", ThumbPath: "/current", Role: "Lead"}
	title := core.Title{Key: key, Type: core.TypeShow, IDs: core.IDs{TMDB: 812}, Title: "Portrait", Released: now.AddDate(-2, 0, 0), Genres: []string{"Drama"}, Keywords: []string{"mystery"}, Countries: []string{"CA"}, Cast: []core.Person{person}, Directors: []core.Person{person}}
	subjects := map[core.RatingKey][]int64{key: {42, 42}}
	var firstQueries, firstRows int64
	for _, extras := range []int{0, 120} {
		title.Cast = []core.Person{person}
		for i := 0; i < extras; i++ {
			title.Cast = append(title.Cast, core.Person{TMDB: int64(1000 + i), Name: fmt.Sprintf("Unrelated %d", i), ThumbPath: "/other"})
		}
		if err := c.UpsertTitle(ctx, title, true); err != nil {
			t.Fatal(err)
		}
		readCtx, stats := store.WithQueryStats(ctx)
		got, err := c.CachedPortraitTitles(readCtx, subjects, config.Catalog{}, now)
		if err != nil {
			t.Fatal(err)
		}
		row := got[key]
		if !reflect.DeepEqual(row.Cast, []core.Person{person}) || !reflect.DeepEqual(row.Directors, []core.Person{person}) || !reflect.DeepEqual(row.Genres, title.Genres) || !reflect.DeepEqual(row.Keywords, title.Keywords) || !reflect.DeepEqual(row.Countries, title.Countries) {
			t.Fatalf("portrait facts: %+v", row)
		}
		if extras == 0 {
			firstQueries, firstRows = stats.Queries(), stats.Rows()
		} else if stats.Queries() != firstQueries || stats.Rows() != firstRows {
			t.Fatalf("unrelated credits increased work: %d/%d -> %d/%d", firstQueries, firstRows, stats.Queries(), stats.Rows())
		}
		if stats.Writes() != 0 {
			t.Fatal("portrait read wrote rows")
		}
		fullCtx, fullStats := store.WithQueryStats(ctx)
		if _, err := c.CachedCreditTitles(fullCtx, []core.RatingKey{key}, config.Catalog{}, now); err != nil {
			t.Fatal(err)
		}
		t.Logf("unrelated credits=%d: full queries/rows=%d/%d; portrait=%d/%d", extras, fullStats.Queries(), fullStats.Rows(), stats.Queries(), stats.Rows())
		if extras > 0 && fullStats.Rows() <= stats.Rows()+100 {
			t.Fatal("fixture did not exercise unrelated full hydration")
		}
	}
	// Check the exact-pair lookup plan, outside the measured request.
	explain := "EXPLAIN "
	if store.DialectOf(db) == store.SQLite {
		explain = "EXPLAIN QUERY PLAN "
	}
	planRows, err := db.QueryContext(ctx, explain+`SELECT cr.key,cr.kind,cr.name,cr.thumb,COALESCE(p.imdb,'') FROM catalog_credits cr LEFT JOIN catalog_people p ON p.person_tmdb=cr.person_tmdb WHERE cr.key=? AND cr.person_tmdb=?`, int64(key), 42)
	if err != nil {
		t.Fatal(err)
	}
	var plan []string
	for planRows.Next() {
		var detail string
		if store.DialectOf(db) == store.SQLite {
			var id, parent, unused int
			err = planRows.Scan(&id, &parent, &unused, &detail)
		} else {
			err = planRows.Scan(&detail)
		}
		if err != nil {
			planRows.Close()
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	err = planRows.Err()
	planRows.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("exact-pair plan: %s", strings.Join(plan, "; "))
	if store.DialectOf(db) == store.SQLite {
		indexedPair := false
		for _, detail := range plan {
			if strings.Contains(detail, "SCAN cr") {
				t.Fatal("credit lookup scans credits")
			}
			// Either the person/key index or the composite primary key can
			// satisfy this exact pair. Require both equality constraints on
			// the indexed SEARCH, independent of the planner's index choice.
			if strings.Contains(detail, "SEARCH cr USING ") && strings.Contains(detail, "key=?") && strings.Contains(detail, "person_tmdb=?") {
				indexedPair = true
			}
		}
		if !indexedPair {
			t.Fatal("credit lookup lost indexed exact-pair access")
		}
	}
	// A full cached read above must not freeze the credit seen by this proof.
	if _, err := db.ExecContext(ctx, `DELETE FROM catalog_credits WHERE key=?`, int64(key)); err != nil {
		t.Fatal(err)
	}
	got, err := c.CachedPortraitTitles(ctx, subjects, config.Catalog{}, now)
	if err != nil || len(got[key].Cast)+len(got[key].Directors) != 0 {
		t.Fatalf("stale credit retained: %+v %v", got, err)
	}
	blocked := config.Catalog{Exclusions: []core.LibraryExclusion{{Name: "genre", MediaType: "show", Match: "all", Rules: []core.CollectionRule{{Field: "genre", Op: "is", Value: json.RawMessage(`"Drama"`)}}}}}
	if got, err := c.CachedPortraitTitles(ctx, subjects, blocked, now); err != nil || len(got) != 0 {
		t.Fatalf("owned policy ignored: %+v %v", got, err)
	}
	if got, err := c.CachedPortraitTitles(ctx, subjects, config.Catalog{}, now.AddDate(-3, 0, 0)); err != nil || len(got) != 0 {
		t.Fatalf("owned clock ignored: %+v %v", got, err)
	}
}
