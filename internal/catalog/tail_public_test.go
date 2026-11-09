package catalog

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// Any authenticated call through this client panics. Public exports must
// still build, including when a previous keyed run left a names cache.
type tailWithoutKey struct{ TMDB }

func (tailWithoutKey) HasKey() bool { return false }

func TestTailPublicExportsWithoutAPIKey(t *testing.T) {
	for _, tc := range []struct {
		name   string
		client TMDB
	}{{"no client", nil}, {"no key", tailWithoutKey{}}} {
		t.Run(tc.name, func(t *testing.T) {
			u := newUniverse()
			c := universeCatalog(t, u, config.Catalog{}, true)
			c.SetTMDB(tc.client)
			c.tail.dir = filepath.Join(t.TempDir(), "empty-data", "tmdb-exports")
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); c.RunTail(ctx) }()
			t.Cleanup(func() { cancel(); <-done; c.installTail(nil) })
			deadline := time.After(10 * time.Second)
			tick := time.NewTicker(10 * time.Millisecond)
			defer tick.Stop()
			for !c.TailStatus().Ready {
				select {
				case <-deadline:
					t.Fatalf("public export did not build: %+v", c.TailStatus())
				case <-tick.C:
				}
			}
			cancel()
			<-done
			if st := c.TailStatus(); !st.Enabled || st.LastError != "" {
				t.Fatalf("public export status: %+v", st)
			}
			if _, ok := c.tailEntry(core.MovieKey(u.movies[0].id)); !ok {
				t.Fatal("public export title missing")
			}
			names := &namesBuilder{}
			names.add(core.MovieKey(u.movies[0].id), "Cached name", time.Time{}, "", 0)
			if err := writeTailNames(filepath.Join(c.tail.dir, tailNamesFile), names.build()); err != nil {
				t.Fatal(err)
			}
			if err := c.buildTail(context.Background()); err != nil {
				t.Fatal(err)
			}
			if entry, ok := c.tailEntry(core.MovieKey(u.movies[0].id)); !ok || entry.Title.Title != "Cached name" {
				t.Fatal("unkeyed rebuild lost cached name")
			}
			if err := c.refreshTail(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := c.getMeta(context.Background(), "tail_names_walked"); got != "" {
				t.Fatal("unkeyed build ran authenticated names walk")
			}
			// Configuring a working client later restores optional enrichment.
			c.SetTMDB(u)
			if err := c.refreshTail(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := c.getMeta(context.Background(), "tail_names_walked"); got == "" {
				t.Fatal("names walk did not resume after client configured")
			}
		})
	}
}
