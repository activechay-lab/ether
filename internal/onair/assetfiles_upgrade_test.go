package onair

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// release035 is the On Air schema level v0.3.5 shipped (onair/4): asset
// bytes in onair_assets.content, no stored column, no replicas.
var release035 = map[string]int{"onair": 4}

// The move-out invariant: from a v0.3.5 database full of reel blobs, an
// upgrade serves every asset at every point of the move, survives kill -9
// mid-move, finishes with no bytes left in the database and the space
// returned, never empties a row while some member cannot read it, and moves
// an older backup's blobs out again. ETHER_ONAIR_UPGRADE_MB sets the reel
// bytes (default 24; use several hundred for the full run). PostgreSQL runs
// when ETHER_TEST_POSTGRES is set.
func TestAssetMoveOutUpgrade(t *testing.T) {
	total := int64(24)
	if v, _ := strconv.Atoi(os.Getenv("ETHER_ONAIR_UPGRADE_MB")); v > 0 {
		total = int64(v)
	}
	t.Run("sqlite", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "ether.db")
		old := openAt(t, store.WithMigrationCeiling(context.Background(), release035), func(ctx context.Context) (*sql.DB, error) { return store.OpenPath(ctx, path) })
		want := insertReels(t, old, total<<20)
		old.Close()
		backup := filepath.Join(dir, "backup-035.db")
		copyFile(t, path, backup)
		db := openAt(t, context.Background(), func(ctx context.Context) (*sql.DB, error) { return store.OpenPath(ctx, path) })
		before := sqliteBytes(t, db)
		blobs := filepath.Join(dir, "assets")
		moveOutWithCrash(t, db, "sqlite:"+path, blobs, want)
		after := sqliteBytes(t, db)
		t.Logf("sqlite: %d MB of reels; database %d MB before the move, %d MB after", total, before>>20, after>>20)
		if after > before-(total<<20)+12<<20 { // ReleaseFreePages keeps 8 MB of free pages for reuse
			t.Fatalf("database kept the reel bytes: %d -> %d", before, after)
		}
		db.Close()
		// An older backup restored over the moved data still has blobs:
		// the move-out runs again and finds the files already there.
		copyFile(t, backup, path)
		db = openAt(t, context.Background(), func(ctx context.Context) (*sql.DB, error) { return store.OpenPath(ctx, path) })
		s := moveService(t, db, blobs, "node-a", nil)
		st, err := s.moveAssets(context.Background())
		if err != nil || st.Copied != 0 || st.Emptied != len(want) {
			t.Fatalf("restored backup move-out: %+v %v", st, err)
		}
		t.Logf("restored v0.3.5 backup, moved out again: %+v", st)
		checkReads(t, s, want)
		db.Close()
	})
	t.Run("postgres", func(t *testing.T) {
		url := os.Getenv(store.TestPostgresEnv)
		if url == "" {
			t.Skip(store.TestPostgresEnv + " not set")
		}
		db, err := store.OpenTestPostgres(store.WithMigrationCeiling(context.Background(), release035), url)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		want := insertReels(t, db, total<<20)
		if err = store.Migrate(context.Background(), db); err != nil {
			t.Fatal("upgrade:", err)
		}
		var schema string
		if err = db.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil {
			t.Fatal(err)
		}
		before := pgBytes(t, db)
		moveOutWithCrash(t, db, "postgres:"+url+"&search_path="+schema, filepath.Join(t.TempDir(), "assets"), want)
		t.Logf("postgres: %d MB of reels; onair_assets %d MB before the move, %d MB after (autovacuum reuses the rest)", total, before>>20, pgBytes(t, db)>>20)
	})
	t.Run("cluster", func(t *testing.T) {
		ctx := context.Background()
		dir := t.TempDir()
		db := openAt(t, ctx, func(ctx context.Context) (*sql.DB, error) { return store.OpenPath(ctx, filepath.Join(dir, "ether.db")) })
		want := insertReels(t, db, 1<<20)
		members := []Member{{ID: "a", Files: true}, {ID: "old", Files: false}}
		list := func(context.Context) ([]Member, error) { return members, nil }
		a := moveService(t, db, filepath.Join(dir, "a"), "a", list)
		b := moveService(t, db, filepath.Join(dir, "b"), "b", list)
		emptied := func() int {
			var n int
			if err := db.QueryRow(`SELECT COUNT(*) FROM onair_assets WHERE stored='file'`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		pass := func(s *Service) {
			if _, err := s.moveAssets(ctx); err != nil {
				t.Fatal(err)
			}
		}
		// An older node still reads content: nothing is emptied.
		pass(a)
		if emptied() != 0 {
			t.Fatal("emptied rows while an older node reads content")
		}
		// It stops for days and drops out of the membership list; b
		// upgrades and copies everything. The older node may come back as
		// it was, so nothing is emptied yet.
		members = []Member{{ID: "a", Files: true}, {ID: "b", Files: true}}
		pass(a)
		pass(b)
		pass(a)
		if emptied() != 0 {
			t.Fatal("emptied rows while an absent older node may return")
		}
		// It returns upgraded and copies them: now every row empties.
		members = append(members, Member{ID: "old", Files: true})
		old := moveService(t, db, filepath.Join(dir, "old"), "old", list)
		pass(old)
		pass(a)
		if emptied() != len(want) {
			t.Fatalf("emptied %d of %d once every member held them", emptied(), len(want))
		}
		// An older node no newer node ever saw arrives: its heartbeat
		// gets the bytes written back, and they stay after it leaves.
		members = append(members, Member{ID: "ghost", Files: false})
		pass(a)
		var inDB int
		if err := db.QueryRow(`SELECT COUNT(*) FROM onair_assets WHERE stored='db' AND length(content)=size`).Scan(&inDB); err != nil || inDB != len(want) {
			t.Fatalf("%d of %d rows written back for an older node (%v)", inDB, len(want), err)
		}
		members = members[:3]
		pass(a)
		pass(b)
		if emptied() != 0 {
			t.Fatal("emptied rows after an older node was seen")
		}
		members = append(members, Member{ID: "ghost", Files: true})
		ghost := moveService(t, db, filepath.Join(dir, "ghost"), "ghost", list)
		pass(ghost)
		pass(a)
		if emptied() != len(want) {
			t.Fatalf("emptied %d of %d after the older node upgraded", emptied(), len(want))
		}
		// A node joins after the move: a holder puts the bytes back, it
		// copies them, and the rows empty again.
		members = append(members, Member{ID: "c", Files: true})
		c := moveService(t, db, filepath.Join(dir, "c"), "c", list)
		done := make(chan struct{})
		go func() { defer close(done); checkReads(t, c, want) }()
		pass(a)
		<-done
		pass(c)
		pass(b)
		if emptied() != len(want) {
			t.Fatalf("emptied %d of %d after the new node copied them", emptied(), len(want))
		}
	})
}

// moveOutWithCrash runs the move-out in a child process, kills it with
// SIGKILL part way, checks every asset still reads, then finishes the move.
func moveOutWithCrash(t *testing.T, db *sql.DB, target, blobs string, want map[string]string) {
	t.Helper()
	ctx := context.Background()
	cmd := exec.Command(os.Args[0], "-test.run=^TestAssetMoveOutChild$", "-test.count=1")
	cmd.Env = append(os.Environ(), "ETHER_ONAIR_MOVE_CHILD="+target, "ETHER_ONAIR_MOVE_BLOBS="+blobs)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	half := len(want) / 2
	deadline := time.Now().Add(5 * time.Minute)
	for {
		var n int
		_ = db.QueryRow(`SELECT COUNT(*) FROM onair_asset_replicas`).Scan(&n)
		if n >= half || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	var dbRows, fileRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM onair_assets WHERE stored='db'`).Scan(&dbRows); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRow(`SELECT COUNT(*) FROM onair_assets WHERE stored='file'`).Scan(&fileRows)
	t.Logf("killed mid-move: %d rows still in the database, %d moved", dbRows, fileRows)
	if dbRows == 0 {
		t.Log("the child finished before it was killed; raise ETHER_ONAIR_UPGRADE_MB for a mid-move kill")
	}
	s := moveService(t, db, blobs, "node-a", nil)
	checkReads(t, s, want)
	st, err := s.moveAssets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("resumed pass: %+v", st)
	var left int64
	if err = db.QueryRow(`SELECT COUNT(*) FROM onair_assets WHERE stored<>'file' OR length(content)>0`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("%d rows still hold bytes (%v)", left, err)
	}
	checkReads(t, s, want)
	if err = s.sweepAssets(ctx); err != nil {
		t.Fatal(err)
	}
	checkReads(t, s, want)
}

// TestAssetMoveOutChild is the process moveOutWithCrash kills.
func TestAssetMoveOutChild(t *testing.T) {
	target := os.Getenv("ETHER_ONAIR_MOVE_CHILD")
	if target == "" {
		t.Skip("run by TestAssetMoveOutUpgrade")
	}
	ctx := context.Background()
	var db *sql.DB
	var err error
	if p, ok := cutPrefix(target, "sqlite:"); ok {
		db, err = store.OpenPath(ctx, p)
	} else if u, ok := cutPrefix(target, "postgres:"); ok {
		db, _, err = store.OpenWith(ctx, store.Options{URL: u})
	}
	if err != nil {
		t.Fatal(err)
	}
	s := moveService(t, db, os.Getenv("ETHER_ONAIR_MOVE_BLOBS"), "node-a", nil)
	for {
		if _, err := s.moveAssets(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func cutPrefix(s, p string) (string, bool) {
	if len(s) >= len(p) && s[:len(p)] == p {
		return s[len(p):], true
	}
	return "", false
}

func openAt(t *testing.T, ctx context.Context, open func(context.Context) (*sql.DB, error)) *sql.DB {
	t.Helper()
	db, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func moveService(t *testing.T, db *sql.DB, blobs, node string, members func(context.Context) ([]Member, error)) *Service {
	t.Helper()
	s, err := New(Options{DB: db, Dir: t.TempDir(), AssetDir: blobs, NodeID: node, Members: members, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// insertReels writes reel sources the way v0.3.5 did, bytes in the row,
// in 32 MB pieces of incompressible bytes. It returns digest -> sha256.
func insertReels(t *testing.T, db *sql.DB, total int64) map[string]string {
	t.Helper()
	const piece = 32 << 20
	size := min(total/8, piece)
	want := map[string]string{}
	r := rand.New(rand.NewPCG(1, 2))
	for left := total; left > 0; left -= size {
		b := make([]byte, min(size, left))
		for i := 0; i+8 <= len(b); i += 8 {
			v := r.Uint64()
			for j := range 8 {
				b[i+j] = byte(v >> (8 * j))
			}
		}
		sum := sha256.Sum256(b)
		d := hex.EncodeToString(sum[:])
		if _, err := db.Exec(`INSERT INTO onair_assets(digest,kind,name,mime,size,license,content,created_at) VALUES(?,'reel',?,'video/mp4',?,'',?,?)`, d, fmt.Sprintf("reel %d", len(want)), len(b), b, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
		want[d] = d
	}
	return want
}

func checkReads(t *testing.T, s *Service, want map[string]string) {
	t.Helper()
	for d := range want {
		p, err := s.assetPath(context.Background(), d)
		if err != nil {
			t.Errorf("asset %s unreadable: %v", d[:8], err)
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			t.Error(err)
			continue
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil || hex.EncodeToString(h.Sum(nil)) != d {
			t.Errorf("asset %s read wrong bytes (%v)", d[:8], err)
		}
	}
}

func sqliteBytes(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var pages, size int64
	if err := db.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`PRAGMA page_size`).Scan(&size); err != nil {
		t.Fatal(err)
	}
	return pages * size
}

func pgBytes(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(`SELECT pg_total_relation_size('onair_assets')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	for _, sfx := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(to + sfx)
	}
	in, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(out, in); err == nil {
		err = out.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
}
