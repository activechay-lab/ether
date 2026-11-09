package catalog

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// Unchanged content with the same freshness timestamp must produce no WAL frames. Set
// ETHER_MEASURE_WRITES=1 for a 10,000-title measurement: actual WAL frames plus
// unique database pages to copy at the final checkpoint. No auto-checkpoint
// runs during the measured pass. This excludes filesystem/SSD amplification.
func TestCatalogWriteVolume(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := New(db, nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := 20
	if os.Getenv("ETHER_MEASURE_WRITES") != "" {
		n = 10000
	}
	titles := make([]core.Title, n)
	for i := range titles {
		titles[i] = core.Title{Key: core.MovieKey(int64(i + 1)), Type: core.TypeMovie, IDs: core.IDs{TMDB: int64(i + 1)}, Title: fmt.Sprintf("Synthetic movie %d", i), Summary: strings.Repeat("A synthetic overview. ", 20), Released: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), Genres: []string{"Drama", "Adventure"}, Popularity: 12, VoteCount: 100}
	}
	for passIndex, pass := range []string{"insert", "unchanged", "jitter", "detail", "detail-unchanged", "detail-refreshed", "unchanged-cursor"} {
		if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			t.Fatal(err)
		}
		stamp := passIndex
		if pass == "detail-unchanged" {
			stamp-- // identical content and identical freshness: a real no-op
		}
		c.clock.Store(func() time.Time {
			return time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC).Add(time.Duration(stamp) * time.Hour)
		})
		opts := upsertOpts{browsable: true}
		if pass == "jitter" {
			for i := range titles {
				titles[i].Popularity++
			}
		}
		if strings.HasPrefix(pass, "detail") {
			opts.detail = true
			for i := range titles {
				titles[i].Keywords = []string{"journey", "friendship"}
				titles[i].Cast = []core.Person{{TMDB: 123, Name: "Actor", Role: "Lead"}}
			}
		}
		resumed, err := c.resumeSync(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i += 20 {
			if err := c.upsertMany(ctx, titles[i:i+20], opts); err != nil {
				t.Fatal(err)
			}
			if pass == "unchanged-cursor" {
				r := resumed.Value(syncResumeKey{}).(*syncResume)
				r.Walks["false/popularity"] = walkCursor{Leaf: "false/popularity/2000-01-01..2000-12-31/0--1", Page: i/20 + 2}
				if err := c.saveSyncResume(resumed); err != nil {
					t.Fatal(err)
				}
			}
		}
		wal, err := os.ReadFile(filepath.Join(dir, "ether.db-wal"))
		if err != nil {
			t.Fatal(err)
		}
		pages := map[uint32]bool{}
		frames := 0
		pageSize := 4096
		if len(wal) >= 32 {
			pageSize = int(binary.BigEndian.Uint32(wal[8:12]))
			for off := 32; off+24+pageSize <= len(wal); off += 24 + pageSize {
				pages[binary.BigEndian.Uint32(wal[off:off+4])] = true
				frames++
			}
		}
		total := len(wal) + len(pages)*pageSize
		if (pass == "unchanged" || pass == "detail-unchanged") && total != 0 {
			t.Fatalf("unchanged %s wrote %d bytes", pass, total)
		}
		t.Logf("%s titles=%d WAL=%d frames=%d checkpoint-page-bytes=%d total=%d bytes/title=%.2f estimate600k=%.0f", pass, n, len(wal), frames, len(pages)*pageSize, total, float64(total)/float64(n), float64(total)*600000/float64(n))
	}
}
