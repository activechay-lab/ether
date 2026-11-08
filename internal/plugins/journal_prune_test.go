package plugins

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// Journal retention never removes a live entry: undo replays those however
// old they are. Only old undone or shadow entries go.
func TestPruneJournalKeepsLiveEntries(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Unix(1_800_000_000, 0)
	st := &storage{db: db, now: func() time.Time { return now }}
	old := now.Add(-JournalSettledMaxAge - time.Hour).Unix()
	for _, e := range []struct {
		op             string
		shadow, undone int
		created        int64
	}{
		{"live-old", 0, 0, old},
		{"undone-old", 0, 1, old},
		{"shadow-old", 1, 0, old},
		{"undone-new", 0, 1, now.Unix()},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO plugins_journal(plugin,op,shadow,undone,created) VALUES('p',?,?,?,?)`, e.op, e.shadow, e.undone, e.created); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := st.pruneJournal(ctx); err != nil || n != 2 {
		t.Fatalf("pruneJournal = %d, %v; want 2", n, err)
	}
	live, err := st.journalList(ctx, "p", 10, true)
	if err != nil || len(live) != 1 || live[0].Op != "live-old" {
		t.Fatalf("live entries after prune = %+v, %v", live, err)
	}
}
