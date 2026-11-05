package catalog

import (
	"context"
	"reflect"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestConsoleCursorTraversesScalarSortsWithoutGaps(t *testing.T) {
	c := largeCatalogue(t, 73)
	ctx := context.Background()
	_, err := c.db.ExecContext(ctx, `UPDATE catalog_titles SET type=CASE WHEN tmdb_id%2=0 THEN 'show' ELSE 'movie' END,year=1990+(tmdb_id%3),rating=(tmdb_id%4),popularity=(tmdb_id%5),runtime_sec=3600+(tmdb_id%3)*60,sort_title='Film ' || (tmdb_id%7),show_added_ts=released_ts`)
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []core.MediaType{"", core.TypeMovie, core.TypeShow} {
		for _, sort := range []core.SortOrder{core.SortPopularity, core.SortTitle, core.SortReleased, core.SortAdded, core.SortYear, core.SortRating, core.SortDuration, core.SortUpdated} {
			for _, desc := range []bool{false, true} {
				q := core.BrowseQuery{Filter: core.Filter{Type: typ}, Sort: sort, Desc: desc, SecondarySorts: []core.SortSpec{{Sort: core.SortTitle, Desc: !desc}}, Limit: 100}
				reference, err := c.Browse(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				want := make([]core.RatingKey, 0, len(reference.Items))
				for _, title := range reference.Items {
					want = append(want, title.Key)
				}
				q.CursorPage = true
				q.NoTotal = true
				q.Filter.LocalOnly = true
				q.Limit = 7
				got := []core.RatingKey{}
				for n := 0; n < 20; n++ {
					page, err := c.Browse(ctx, q)
					if err != nil {
						t.Fatalf("%s %s %v: %v", typ, sort, desc, err)
					}
					for _, title := range page.Items {
						got = append(got, title.Key)
					}
					if len(page.Items) < q.Limit {
						break
					}
					q.AfterKey = page.Items[len(page.Items)-1].Key
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s %s desc=%v: got %v want %v", typ, sort, desc, got, want)
				}
			}
		}
	}
}

func TestConsoleCursorRejectsOffsetAndUnavailableAnchor(t *testing.T) {
	c := largeCatalogue(t, 3)
	q := core.BrowseQuery{CursorPage: true, NoTotal: true, Filter: core.Filter{LocalOnly: true}, Offset: 100000}
	if _, err := c.Browse(context.Background(), q); err == nil {
		t.Fatal("cursor allowed OFFSET")
	}
	q.Offset = 0
	q.AfterKey = core.MovieKey(999999)
	if _, err := c.Browse(context.Background(), q); err == nil {
		t.Fatal("missing cursor anchor silently changed position")
	}
}

func TestConsoleSeekHasLeadingIndexBoundAndMixedDirections(t *testing.T) {
	sql, args := consoleSeek([]string{"t.year", "t.sort_title"}, []core.SortSpec{{Sort: core.SortYear, Desc: true}, {Sort: core.SortTitle}}, []any{int64(1995), "Seven"}, 7)
	want := " AND t.year<=? AND ((t.year<?) OR (t.year=? AND t.sort_title>?) OR (t.year=? AND t.sort_title=? AND t.key>?))"
	if sql != want {
		t.Fatal(sql)
	}
	if !reflect.DeepEqual(args, []any{int64(1995), int64(1995), int64(1995), "Seven", int64(1995), "Seven", int64(7)}) {
		t.Fatal(args)
	}
}
