package store

import (
	"context"
	"strconv"
	"testing"
)

// ANALYZE reloads statistics only on its own connection. A pooled
// connection that read the schema before must plan with the new
// statistics once they are refreshed, not with the ones it loaded.
func TestRefreshedStatisticsReachEveryPooledConnection(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TABLE stats_t(id INTEGER PRIMARY KEY, type INT, show INT, x INT);
		CREATE INDEX stats_t_type ON stats_t(type, x); CREATE INDEX stats_t_show ON stats_t(show);
		WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<40000) INSERT INTO stats_t SELECT i, i%4, i/10, i FROM n`); err != nil {
		t.Fatal(err)
	}
	held, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	plans := 0
	plan := func() string {
		t.Helper()
		plans++
		// A statement that reads starts a transaction, which is when a
		// connection notices a schema change; EXPLAIN alone does not, and
		// a cached EXPLAIN keeps its plan, so each one is new text.
		var one int
		if err := held.QueryRowContext(ctx, `SELECT 1 FROM stats_t LIMIT 1`).Scan(&one); err != nil {
			t.Fatal(err)
		}
		var id, parent, unused int
		var detail string
		if err := held.QueryRowContext(ctx, `EXPLAIN QUERY PLAN SELECT * FROM stats_t WHERE type=1 AND show IN (1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20) ORDER BY x -- `+strconv.Itoa(plans)).Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		return detail
	}
	if p := plan(); p != "SEARCH stats_t USING INDEX stats_t_type (type=?)" {
		t.Fatalf("without statistics: %s", p)
	}
	if err := Maintain(ctx, db); err != nil {
		t.Fatal(err)
	}
	if p := plan(); p != "SEARCH stats_t USING INDEX stats_t_show (show=?)" {
		t.Fatalf("held connection still plans without the new statistics: %s", p)
	}
}
