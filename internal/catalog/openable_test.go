package catalog_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// A remote list and a direct lookup must agree even when the list's release
// summary conflicts with detailed cinema/home dates, or is not yet mirrored.
func TestListAndDetailAdmissionInvariant(t *testing.T) {
	for _, mode := range []string{config.ModeImmediate, config.ModeDigital} {
		t.Run(mode, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var id int64
				if _, err := fmt.Sscanf(r.URL.Path, "/movie/%d", &id); err != nil {
					http.NotFound(w, r)
					return
				}
				theatrical := "2099-05-14T00:00:00Z"
				digital := "2099-10-22"
				if id == 1662860 {
					theatrical = "2001-05-14T00:00:00Z"
					digital = "2001-10-22"
				}
				if id == 1662858 {
					digital = "2001-10-22"
				}
				fmt.Fprintf(w, `{"id":%d,"title":"EB fixture","release_date":"2001-05-14","poster_path":"/eb.jpg","release_dates":{"results":[{"iso_3166_1":"NL","release_dates":[{"type":3,"release_date":%q},{"type":4,"release_date":%q}]}]}}`, id, theatrical, digital)
			}))
			defer srv.Close()
			c := newTestCatalogCfg(t, srv, config.Catalog{Region: "NL", ReleaseMode: mode})
			ctx := context.Background()
			key := core.MovieKey(1662858)
			movie, err := c.Get(ctx, key)
			if err != nil {
				t.Fatalf("past NL digital date must admit future theatrical date: %v", err)
			}
			if movie.Released.Year() != 2099 || !movie.HomeReleaseChecked || !movie.IsReleased(time.Now()) {
				t.Fatalf("release evidence/date-added lost: %+v", movie)
			}
			// A stale list summary cannot override the mirror's deliberate future gate.
			hidden := core.MovieKey(1662859)
			_, _ = c.Get(ctx, hidden)
			summary := core.Title{Key: hidden, Type: core.TypeMovie, Title: "Stale", Released: time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)}
			missing := summary
			missing.Key = core.MovieKey(1662860)
			missing.PosterPath = "/list.jpg"
			got := c.OpenableTitles(ctx, []core.Title{*movie, summary, missing})
			// In digital mode the unknown home date remains hidden (no permissive fallback
			// for recent/unknown cinema dates is introduced by list admission).
			if len(got) != 2 || got[0].Key != key || got[1].Key != missing.Key {
				t.Fatalf("row admission: %+v", got)
			}
			mirrored, err := c.StoredTitle(ctx, missing.Key)
			if err != nil || mirrored.PosterPath != "/list.jpg" {
				t.Fatalf("missing list summary not mirrored: %+v %v", mirrored, err)
			}
			opened, err := c.Get(ctx, missing.Key)
			if err != nil || opened == nil || opened.PosterPath != "/eb.jpg" {
				t.Fatalf("on-demand detail: %+v %v", opened, err)
			}

		})
	}
}
