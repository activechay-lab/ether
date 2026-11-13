package plex

import (
	"encoding/json"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plexmeta"
)

// Online navigation must never turn a namesake's label into another work's
// identity. Cover search and the detail it opens, with and without a verified
// provider slug; cached overlays for an obsolete GUID must not supply one.
func TestSearchNamesakeIdentity(t *testing.T) {
	f, meta := plexMetaFixture(t, true, &plexMetaRoundTrip{body: `{}`})
	years := []int{1999, 2017}
	keys := []core.RatingKey{core.MovieKey(564), core.MovieKey(282035)}
	guids := []string{"plex://movie/5d7768264de0ee001fcc87e1", "plex://movie/5d776aff7a53e9001e71650e"}
	slugs := []string{"the-mummy-1999", "the-mummy-2017"}
	for i, k := range keys {
		title := fightClub
		title.Key, title.Title, title.Year = k, "The Mummy", years[i]
		f.catalog.titles[k] = title
		if err := storeVerifiedGUID(f.srv.runtimeDB(), k, guids[i], int64(k.TMDB()), title.Title, years[i]); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []string{"missing", "verified", "obsolete"} {
		// Finish fetches scheduled by the previous pass before replacing its
		// rows. The empty fake provider response must not race these fixtures
		// and overwrite a verified slug after it has been inserted.
		meta.Wait()
		if mode != "missing" {
			for i, k := range keys {
				_, err := f.srv.runtimeDB().Exec(`DELETE FROM plexmeta_titles WHERE rating_key=?`, int64(k))
				if err != nil {
					t.Fatal(err)
				}
				guid := guids[i]
				if mode == "obsolete" {
					guid = plexMetaGUID
				}
				storePlexOverlay(t, f.srv.runtimeDB(), k, guid, plexmeta.Overlay{Slug: slugs[i]})
			}
		}
		for _, path := range []string{"/library/search?query=mummy", "/hubs/search?query=mummy", "/library/metadata/" + keys[1].String()} {
			rec := f.get(t, path, jsonAccept...)
			if rec.Code != 200 {
				t.Fatalf("%s: %d", path, rec.Code)
			}
			var doc any
			if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
				t.Fatal(err)
			}
			seen := 0
			var walk func(any)
			walk = func(v any) {
				switch row := v.(type) {
				case map[string]any:
					for i, k := range keys {
						if row["ratingKey"] == k.String() {
							seen++
							if row["year"] != float64(years[i]) || row["guid"] != guids[i] || row["key"] != "/library/metadata/"+k.String() {
								t.Fatalf("mixed identity: %#v", row)
							}
							want := ""
							if mode == "verified" {
								want = slugs[i]
							}
							got, _ := row["slug"].(string)
							if got != want {
								t.Fatalf("%s %s slug=%q want %q", mode, path, got, want)
							}
						}
					}
					for _, x := range row {
						walk(x)
					}
				case []any:
					for _, x := range row {
						walk(x)
					}
				}
			}
			walk(doc)
			want := 2
			if path[0:18] == "/library/metadata/" {
				want = 1
			}
			if seen != want {
				t.Fatalf("%s saw %d namesakes want %d", path, seen, want)
			}
		}
	}
}
