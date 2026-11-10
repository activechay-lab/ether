package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/store"
)

type batchProofArtwork struct {
	frontendtest.Artwork
}

func (batchProofArtwork) List(context.Context, frontend.ArtworkRequest) ([]frontend.Image, bool, error) {
	return []frontend.Image{{Kind: frontend.ImagePoster, Path: "poster"}}, true, nil
}
func (batchProofArtwork) Read(context.Context, frontend.ArtworkRequest, frontend.Image, frontend.ArtworkTransform) ([]byte, string, error) {
	panic("proof tried to fetch artwork")
}
func (batchProofArtwork) ListPage(_ context.Context, _ *core.Viewer, _ core.Device, titles []core.Title) (map[core.RatingKey][]frontend.Image, error) {
	out := map[core.RatingKey][]frontend.Image{}
	for _, title := range titles {
		out[title.Key] = []frontend.Image{{Kind: frontend.ImagePoster, Path: "poster"}}
	}
	return out, nil
}

// Real catalogue and experience authorization, with only the poster renderer
// mocked: count the cold SQL which IssueBatch's selected capability invokes.
func TestArtworkPageAuthorizationWorkBound(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	t.Logf("backend: %s", store.DialectOf(db))
	newCatalog := func() *catalog.Catalog {
		c, err := catalog.New(db, nil, config.Catalog{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	seed := newCatalog()
	var keys []core.RatingKey
	for i := int64(1); i <= 26; i++ {
		title := core.Title{Key: core.MovieKey(i), Type: core.TypeMovie, IDs: core.IDs{TMDB: i}, Title: fmt.Sprint("Star ", i), Released: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), Genres: []string{"Drama"}, PosterPath: "/p.jpg"}
		for person := int64(1); person <= 5; person++ {
			title.Cast = append(title.Cast, core.Person{TMDB: person, Name: fmt.Sprint(person)})
		}
		if err := seed.UpsertTitle(ctx, title, true); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, title.Key)
	}
	viewer := &core.User{ID: 1, IsOwner: true}
	surface := func() *surfaceExperience {
		c := newCatalog() // cold cache over identical stored rows
		svc, err := experience.NewService(experience.ServiceDeps{
			Active: func(context.Context, int64) (*core.User, error) { return viewer, nil },
			Get: func(ctx context.Context, _ *core.User, key core.RatingKey) (*core.Title, error) {
				return c.CachedGet(ctx, key)
			},
			CachedGet: c.CachedGet, CachedGetMany: c.CachedGetMany, ArtworkGet: c.ArtworkTitle, ArtworkGetMany: c.ArtworkTitles,
			Children: func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) { return nil, nil },
			State:    searchSummaryState{}, Rows: &experience.RowsService{}, Protocol: "stremio",
			Visibility: experience.Visibility{SectionFor: func(core.MediaType) (int, bool) { return 1, true }, GetTitle: c.CachedGet},
			Search:     experience.Search{Catalog: c.Search},
		})
		if err != nil {
			t.Fatal(err)
		}
		return &surfaceExperience{Service: svc, protocol: "stremio", artwork: batchProofArtwork{}}
	}
	var firstBatch, firstScalar int64
	for _, n := range []int{2, 26} {
		scalar := surface()
		scalarCtx, scalarStats := store.WithQueryStats(ctx)
		for _, key := range keys[:n] {
			images, complete, err := scalar.ItemArtwork(scalarCtx, viewer, core.Device{}, key)
			if err != nil || !complete || len(images) != 1 {
				t.Fatalf("scalar: %v", err)
			}
		}
		page := surface()
		pageCtx, pageStats := store.WithQueryStats(ctx)
		images, err := page.ItemArtworkPage(pageCtx, viewer, core.Device{}, keys[:n])
		if err != nil || len(images) != n {
			t.Fatalf("page: %d images %v", len(images), err)
		}
		if scalarStats.Writes() != 0 || pageStats.Writes() != 0 {
			t.Fatal("artwork authorization wrote catalogue rows")
		}
		t.Logf("titles=%d scalar queries/rows=%d/%d; page=%d/%d", n, scalarStats.Queries(), scalarStats.Rows(), pageStats.Queries(), pageStats.Rows())
		if n == 2 {
			firstBatch, firstScalar = pageStats.Queries(), scalarStats.Queries()
		} else {
			if pageStats.Queries() != firstBatch || pageStats.Queries() > 4 {
				t.Fatal("page authorization SQL grew per title")
			}
			if scalarStats.Queries()-firstScalar != 72 {
				t.Fatal("fixture did not reproduce 24 extra title/facts/trailer triplets")
			}
		}
	}
}

// A grant's page and the image reads after it judge movies and shows from
// their rows (frontend.WithArtworkRows), the image reads finding the rows
// the grant read: together they read less than the grant's full titles did,
// in no more statements, and no credits.
func TestArtworkGrantAndImageReadsReadRows(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	newCatalog := func() *catalog.Catalog {
		c, err := catalog.New(db, nil, config.Catalog{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	seed := newCatalog()
	var keys []core.RatingKey
	for i := int64(1); i <= 26; i++ {
		title := core.Title{Key: core.MovieKey(i), Type: core.TypeMovie, IDs: core.IDs{TMDB: i}, Title: fmt.Sprint("Star ", i), Released: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), Genres: []string{"Drama"}, Keywords: []string{"space"}, PosterPath: "/p.jpg"}
		for person := int64(1); person <= 12; person++ {
			title.Cast = append(title.Cast, core.Person{TMDB: person, Name: fmt.Sprint(person)})
		}
		if err := seed.UpsertTitle(ctx, title, true); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, title.Key)
	}
	viewer := &core.User{ID: 1, IsOwner: true}
	surface := func(rows bool) *surfaceExperience {
		c := newCatalog() // cold cache over identical stored rows
		deps := experience.ServiceDeps{
			Active: func(context.Context, int64) (*core.User, error) { return viewer, nil },
			Get: func(ctx context.Context, _ *core.User, key core.RatingKey) (*core.Title, error) {
				return c.CachedGet(ctx, key)
			},
			CachedGet: c.CachedGet, CachedGetMany: c.CachedGetMany, ArtworkGet: c.ArtworkTitle, ArtworkGetMany: c.ArtworkTitles,
			Children: func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) { return nil, nil },
			State:    searchSummaryState{}, Rows: &experience.RowsService{}, Protocol: "stremio",
			Visibility: experience.Visibility{SectionFor: func(core.MediaType) (int, bool) { return 1, true }, GetTitle: c.CachedGet},
			Search:     experience.Search{Catalog: c.Search},
		}
		if rows {
			deps.ArtworkRows = c.ArtworkRows
		}
		svc, err := experience.NewService(deps)
		if err != nil {
			t.Fatal(err)
		}
		return &surfaceExperience{Service: svc, protocol: "stremio", artwork: batchProofArtwork{}}
	}
	measure := func(rows bool) (queries, read int64) {
		s := surface(rows)
		mark := func(ctx context.Context) context.Context {
			if rows {
				return frontend.WithArtworkRows(ctx)
			}
			return ctx
		}
		grantCtx, stats := store.WithQueryStats(ctx)
		if images, err := s.ItemArtworkPage(mark(grantCtx), viewer, core.Device{}, keys); err != nil || len(images) != len(keys) {
			t.Fatalf("grant: %d images %v", len(images), err)
		}
		for _, key := range keys {
			imageCtx := mark(grantCtx)
			if images, complete, err := s.ItemArtwork(imageCtx, viewer, core.Device{}, key); err != nil || !complete || len(images) != 1 {
				t.Fatalf("image read %v: %v", key, err)
			}
		}
		if stats.Writes() != 0 {
			t.Fatal("artwork authorization wrote")
		}
		return stats.Queries(), stats.Rows()
	}
	fullQ, fullRows := measure(false)
	rowQ, rowRows := measure(true)
	t.Logf("grant + %d image reads: full titles %d queries / %d rows; rows %d / %d", len(keys), fullQ, fullRows, rowQ, rowRows)
	if rowQ > fullQ || rowRows >= fullRows || rowRows > int64(len(keys)) {
		t.Fatalf("rows read %d queries / %d rows, full titles %d / %d", rowQ, rowRows, fullQ, fullRows)
	}
}
