package debrid

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/store"
)

// Prune removes only expired cache rows; a row that remembers a torrent on
// the account or holds a live add claim stays however old its answer is.
func TestPruneKeepsTorrentIDsAndLiveClaims(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Unix(1_800_000_000, 0)
	s := NewService(config.Debrid{}, Options{DB: db, Now: func() time.Time { return now }})
	old := now.Add(-30 * 24 * time.Hour).Unix()
	fresh := now.Add(-time.Hour).Unix()
	for _, r := range []struct {
		hash, tid string
		checked   int64
		claimedAt int64
	}{
		{"expired", "", old, 0},
		{"has-torrent", "t1", old, 0},
		{"live-claim", "", 0, now.Unix()},
		{"fresh", "", fresh, 0},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO debrid_torrents(provider_id,info_hash,torrent_id,cached,checked_at,claimed_by,claimed_at) VALUES('p',?,?,1,?,?,?)`,
			r.hash, r.tid, r.checked, map[bool]string{true: "node", false: ""}[r.claimedAt > 0], r.claimedAt); err != nil {
			t.Fatal(err)
		}
	}
	for key, at := range map[string]int64{"old": old, "new": fresh} {
		if _, err := db.ExecContext(ctx, `INSERT INTO debrid_payloads(release_key,payload,updated_at) VALUES(?,'{}',?)`, key, at); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.Prune(ctx)
	if err != nil || n != 2 {
		t.Fatalf("Prune = %d, %v; want 2 rows", n, err)
	}
	rows, err := db.QueryContext(ctx, `SELECT info_hash FROM debrid_torrents ORDER BY info_hash`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var h string
		_ = rows.Scan(&h)
		got = append(got, h)
	}
	if want := "[fresh has-torrent live-claim]"; fmt.Sprint(got) != want {
		t.Fatalf("kept %v, want %s", got, want)
	}
	var key string
	if err := db.QueryRowContext(ctx, `SELECT release_key FROM debrid_payloads`).Scan(&key); err != nil || key != "new" {
		t.Fatalf("payload survivor %q, %v", key, err)
	}
}
