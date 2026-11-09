package playback

import (
	"context"
	"maps"
	"os"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func newTestResumeStore(t *testing.T) *resumeStore {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	rs, err := newResumeStore(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rs.close(context.Background()) })
	return rs
}

// TestGCKeepsNZBOfRecordBeingWritten: one record drops the last committed
// reference to an NZB while another record for the same NZB found the file
// on disk but has not inserted its row yet. The file must survive, or the
// new row points at a deleted NZB.
func TestGCKeepsNZBOfRecordBeingWritten(t *testing.T) {
	ctx := context.Background()
	rs := newTestResumeStore(t)
	nzb := []byte("<nzb>shared</nzb>")
	rec := func(user int64, id string) resumeRecord {
		return resumeRecord{userID: user, key: 42, release: core.Release{ID: id}, updatedAt: time.Now()}
	}
	if err := rs.save(ctx, rec(1, "shared"), nzb); err != nil {
		t.Fatal(err)
	}
	// User 2's save of the same NZB is in flight: file found, row pending.
	lease, err := rs.acquireNZB(ctx, rec(2, "shared"), nzb)
	if err != nil {
		t.Fatal(err)
	}
	// User 1 switches release: the shared NZB loses its only committed row.
	if err := rs.save(ctx, rec(1, "other"), []byte("<nzb>other</nzb>")); err != nil {
		t.Fatal(err)
	}
	if err := rs.writeBundle(ctx, rec(2, "shared"), lease, nil, core.MediaInfo{}, nzb); err != nil {
		t.Fatal(err)
	}
	rs.releaseNZB(lease)
	if _, err := rs.readNZB(lease.hash); err != nil {
		t.Fatalf("NZB of user 2's record was deleted: %v", err)
	}
	// Once nothing references it, it is collected.
	if err := rs.delete(ctx, 2, 42); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rs.nzbPath(lease.hash)); !os.IsNotExist(err) {
		t.Fatalf("unreferenced NZB kept: %v", err)
	}
}

// TestDeleteDropsKeyframeIndex: an expired resume record takes its
// keyframe index with it; nothing else would ever prune it.
func TestDeleteDropsKeyframeIndex(t *testing.T) {
	ctx := context.Background()
	rs := newTestResumeStore(t)
	r := resumeRecord{userID: 3, key: 77, release: core.Release{ID: "rel"}, updatedAt: time.Now()}
	if err := rs.put(ctx, r, []byte("<nzb/>"), []core.KeyframePoint{{Offset: 10, Time: time.Second}}); err != nil {
		t.Fatal(err)
	}
	if pts, err := rs.loadIndex(ctx, 3, 77, "rel"); err != nil || len(pts) != 1 {
		t.Fatalf("index before delete = %v, %v", pts, err)
	}
	if err := rs.delete(ctx, 3, 77); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := rs.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playback_index`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d keyframe index rows left after delete (%v)", n, err)
	}
}

// TestOrphanSweepKeepsNamedAndLiveRows: the orphan sweep deletes an index or
// probe row only when no resume row names its release, no session of that
// viewer's title is open, the process holds no unsaved resume for that
// release, and the previous sweep already saw it orphaned.
func TestOrphanSweepKeepsNamedAndLiveRows(t *testing.T) {
	ctx := context.Background()
	rs := newTestResumeStore(t)
	pts := []core.KeyframePoint{{Offset: 10, Time: time.Second}}
	orphan := func(user int64, key core.RatingKey, rel string) {
		t.Helper()
		if err := rs.saveIndex(ctx, user, key, rel, pts); err != nil {
			t.Fatal(err)
		}
		if _, err := rs.db.ExecContext(ctx, `INSERT INTO playback_probe(user_id,rating_key,release_id,info_json,updated_at) VALUES(?,?,?,'{}',0)`, user, int64(key), rel); err != nil {
			t.Fatal(err)
		}
	}
	// Named by its resume row.
	named := resumeRecord{userID: 1, key: 10, release: core.Release{ID: "a"}, updatedAt: time.Now()}
	if err := rs.put(ctx, named, []byte("<nzb>a</nzb>"), pts); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.db.ExecContext(ctx, `INSERT INTO playback_probe(user_id,rating_key,release_id,info_json,updated_at) VALUES(1,10,'a','{}',0)`); err != nil {
		t.Fatal(err)
	}
	orphan(1, 10, "b") // another release of a named title
	orphan(2, 20, "c") // a session of this title is open
	orphan(3, 30, "d") // the unsaved resume this process holds
	rs.noteLive(resumeRecord{userID: 3, key: 30, release: core.Release{ID: "d"}, updatedAt: time.Now()}, []byte("<nzb>d</nzb>"))
	orphan(3, 30, "e") // another release of that title
	sessions := map[resumeKey]bool{{2, 20}: true}

	type row struct {
		user int64
		key  int64
		rel  string
	}
	left := func(table string) map[row]bool {
		t.Helper()
		rows, err := rs.db.QueryContext(ctx, `SELECT user_id, rating_key, release_id FROM `+table)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[row]bool{}
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.user, &r.key, &r.rel); err != nil {
				t.Fatal(err)
			}
			out[r] = true
		}
		return out
	}
	all := map[row]bool{{1, 10, "a"}: true, {1, 10, "b"}: true, {2, 20, "c"}: true, {3, 30, "d"}: true, {3, 30, "e"}: true}
	kept := map[row]bool{{1, 10, "a"}: true, {2, 20, "c"}: true, {3, 30, "d"}: true}
	for sweep, want := range []map[row]bool{all, kept, kept} {
		n, err := rs.sweepOrphans(ctx, sessions)
		if err != nil {
			t.Fatal(err)
		}
		if wantN := int64(2 * (len(all) - len(want))); sweep == 1 && n != wantN || sweep != 1 && n != 0 {
			t.Fatalf("sweep %d deleted %d rows", sweep+1, n)
		}
		for _, table := range orphanTables {
			if got := left(table); !maps.Equal(got, want) {
				t.Fatalf("sweep %d: %s = %v, want %v", sweep+1, table, got, want)
			}
		}
	}
}
