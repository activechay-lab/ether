package catalog_test

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
			loaded := c.LoadRowFacts(ctx, []core.Title{*movie, summary, missing})
			var got []core.Title
			for _, title := range loaded {
				if c.TitleOpenable(&title) {
					got = append(got, title)
				}
			}
			// The known future title is hidden; the old missing summary retains age admission.
			if len(got) != 2 || got[0].Key != key || got[1].Key != missing.Key {
				t.Fatalf("row admission: %+v", got)
			}
			opened, err := c.Get(ctx, missing.Key)
			if err != nil || opened == nil {
				t.Fatalf("read-through detail: %+v %v", opened, err)
			}
			c.WaitRefreshes()
			mirrored, err := c.StoredTitle(ctx, missing.Key)
			if err != nil || mirrored.PosterPath == "" {
				t.Fatalf("missing list summary not mirrored: %+v %v", mirrored, err)
			}
			_, _ = c.Get(ctx, missing.Key)
			c.WaitRefreshes()
			opened, err = c.Get(ctx, missing.Key)
			if err != nil || opened == nil || opened.PosterPath != "/eb.jpg" {
				t.Fatalf("on-demand detail: %+v %v", opened, err)
			}
		})
	}
}

// Holding SQLite's writer must not block rendering or opening an unmirrored
// source title, and neither the read nor its pure predicate can write.
func TestListSummaryReadThroughWithoutWriter(t *testing.T) {
	db, err := store.OpenPath(context.Background(), filepath.Join(t.TempDir(), "summary.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := catalog.New(db, nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO catalog_meta(k,v) VALUES('writer-held','1')`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ctx, stats := store.WithQueryStats(ctx)
	titles := []core.Title{{Key: core.MovieKey(1662858), Type: core.TypeMovie, Title: "EB", Released: time.Date(2001, 5, 14, 0, 0, 0, 0, time.UTC), PosterPath: "/list.jpg"}, {Key: core.ShowKey(55), Type: core.TypeShow, Title: "Series", Released: time.Date(2001, 5, 14, 0, 0, 0, 0, time.UTC)}}
	loaded := c.LoadRowFacts(ctx, titles)
	if len(loaded) != len(titles) {
		t.Fatalf("missing summaries: %+v", loaded)
	}
	for _, title := range loaded {
		before := stats.Queries()
		if !c.TitleOpenable(&title) || stats.Queries() != before {
			t.Fatal("predicate performs IO or rejected source summary")
		}
		opened, err := c.Get(ctx, title.Key)
		if err != nil || opened.Title != title.Title {
			t.Fatalf("summary lookup: %+v %v", opened, err)
		}
	}
	if stats.Writes() != 0 {
		t.Fatalf("foreground writes=%d", stats.Writes())
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	c.WaitRefreshes()
	for _, title := range titles {
		if _, err = c.StoredTitle(context.Background(), title.Key); err != nil {
			t.Fatal(err)
		}
	}
}
