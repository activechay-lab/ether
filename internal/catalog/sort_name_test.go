package catalog

import (
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// Jellyfin and Emby's alphabet jump pages the library's sort order: a
// letter (NameStartsWith), the "#" bucket (NameLessThan=A) and "from here
// on" (NameStartsWithOrGreater), in either direction, page by page.
func TestSortNameBoundsPageTheSortOrder(t *testing.T) {
	c, err := New(store.OpenTest(t), nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	for i, title := range []string{"The Batman", "Batman Begins", "beetlejuice", "Æon Flux", "Amélie", "12 Angry Men", "[REC]", "Zodiac", "千と千尋の神隠し", "Ébola Syndrome", "Theory of Everything", "A"} {
		ti := core.Title{Key: core.MovieKey(int64(i + 1)), Type: core.TypeMovie, Title: title, SortTitle: sortTitle(title), Released: time.Now().Add(-time.Hour)}
		if err := c.upsert(ctx, ti, upsertOpts{browsable: true}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		f    core.Filter
		desc bool
		want string
	}{
		{core.Filter{NameStartsWith: "B"}, false, "[batman batman begins beetlejuice]"},
		{core.Filter{NameStartsWith: "b"}, true, "[beetlejuice batman begins batman]"},
		{core.Filter{NameStartsWith: "batman "}, false, "[batman begins]"},
		{core.Filter{NameStartsWith: "AE"}, false, "[aeon flux]"},
		{core.Filter{NameStartsWith: "É"}, false, "[ebola syndrome]"},
		{core.Filter{NameStartsWith: "the"}, false, "[theory of everything]"},
		{core.Filter{NameLessThan: "A"}, false, "[12 angry men [rec]]"},
		{core.Filter{NameStartsWithOrGreater: "T"}, false, "[theory of everything zodiac 千と千尋の神隠し]"},
		{core.Filter{NameStartsWithOrGreater: "b", NameLessThan: "E"}, false, "[batman batman begins beetlejuice]"},
		{core.Filter{NameStartsWith: "Z", NameLessThan: "B"}, false, "[]"},
		{core.Filter{NameStartsWith: "  "}, false, "[12 angry men [rec] a aeon flux amelie batman batman begins beetlejuice ebola syndrome theory of everything zodiac 千と千尋の神隠し]"},
		{core.Filter{FirstCharacter: "#"}, false, "[12 angry men [rec]]"},
		{core.Filter{FirstCharacter: "~"}, false, "[千と千尋の神隠し]"},
	} {
		tc.f.Type = core.TypeMovie
		var got []string
		total := -1
		for offset := 0; ; offset += 2 {
			page, err := c.Browse(ctx, core.BrowseQuery{Filter: tc.f, Sort: core.SortTitle, Desc: tc.desc, Offset: offset, Limit: 2})
			if err != nil {
				t.Fatalf("%+v: %v", tc.f, err)
			}
			if total >= 0 && page.Total != total {
				t.Fatalf("%+v: total moved from %d to %d", tc.f, total, page.Total)
			}
			total = page.Total
			for _, item := range page.Items {
				got = append(got, item.SortTitle)
			}
			if len(page.Items) < 2 {
				break
			}
		}
		if fmt.Sprint(got) != tc.want || total != len(got) {
			t.Errorf("%+v desc=%v: %v (total %d), want %s", tc.f, tc.desc, got, total, tc.want)
		}
	}
}
