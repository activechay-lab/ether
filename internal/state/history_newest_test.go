package state

import (
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// The index-ordered read of a viewer's newest history returns exactly the
// rows and order of the SQL sort it replaces, ties within a second included.
func TestNewestUserHistoryMatchesSQLOrder(t *testing.T) {
	s, _ := newTestState(t)
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 600; i++ {
		user := int64(1 + i%2)
		// Few distinct seconds, so pages end inside a run of ties.
		at := (1_700_000_000+int64(rng.IntN(40)))*1_000_000_000 + int64(rng.IntN(1_000_000_000))
		if _, err := s.db.ExecContext(ctx, `INSERT INTO state_history(user_id,rating_key,viewed_at,completed,view_type) VALUES(?,?,?,?,?)`, user, int64(core.MovieKey(int64(i+1))), at, i%2, i%2); err != nil {
			t.Fatal(err)
		}
	}
	since, until := int64(1_700_000_005), int64(1_700_000_030)
	for _, q := range []core.HistoryQuery{
		{UserID: 1, ViewType: -1, Limit: 7},
		{UserID: 1, ViewType: -1, Limit: 50, Offset: 33},
		{UserID: 2, ViewType: -1, Limit: 500},
		{UserID: 2, ViewType: -1, Limit: 20, Offset: 290},
		{UserID: 1, ViewType: -1, Limit: 25, Offset: 10, Since: &since, Until: &until},
	} {
		got, err := s.QueryHistory(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		where, args := "user_id=?", []any{q.UserID}
		if q.Since != nil {
			where, args = where+" AND viewed_at/1000000000>=?", append(args, *q.Since)
		}
		if q.Until != nil {
			where, args = where+" AND viewed_at/1000000000<=?", append(args, *q.Until)
		}
		rows, err := s.db.QueryContext(ctx, "SELECT id FROM state_history WHERE "+where+" ORDER BY viewed_at/1000000000 DESC, id ASC LIMIT ? OFFSET ?", append(args, q.Limit, q.Offset)...)
		if err != nil {
			t.Fatal(err)
		}
		var want, have []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			want = append(want, id)
		}
		rows.Close()
		for _, h := range got {
			have = append(have, h.ID)
		}
		if !slices.Equal(have, want) {
			t.Fatalf("%+v:\n have %v\n want %v", q, have, want)
		}
	}
}
