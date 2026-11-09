package service

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// UltraBlur colours of a title with no row (a search hit) stay in memory;
// a stored title's are written.
func TestPaletteWritesOnlyStoredTitles(t *testing.T) {
	db := store.OpenTest(t)
	run := func(_ string, fn func(context.Context)) bool { fn(context.Background()); return true }
	c := NewPaletteCache(db, run, time.Now, slog.Default(), func(context.Context, core.RatingKey, string) (Palette, error) {
		return Palette{TopLeft: "111111", TopRight: "222222", BottomLeft: "333333", BottomRight: "444444"}, nil
	})
	stored := core.MovieKey(1)
	c.PersistFor(func(_ context.Context, key core.RatingKey) bool { return key == stored })
	for _, key := range []core.RatingKey{stored, core.MovieKey(2)} {
		c.Lookup(key, "/b.jpg")
		if c.Lookup(key, "/b.jpg") == nil {
			t.Fatalf("%v: colours not kept in memory", key)
		}
	}
	var keys []int64
	rows, err := db.Query(`SELECT key FROM plex_ultrablur`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k int64
		_ = rows.Scan(&k)
		keys = append(keys, k)
	}
	rows.Close()
	if len(keys) != 1 || keys[0] != int64(stored) {
		t.Fatalf("plex_ultrablur keys = %v, want only the stored title %d", keys, int64(stored))
	}
}
