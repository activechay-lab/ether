package livetv

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// The optional 16k/1.6m run uses the same assertions as the fast CI fixture.
// Result-row counters are paired with SQLite's query plan so a full table scan
// cannot hide behind a small result set.
func TestGuideIndexedScale(t *testing.T) {
	ctx := context.Background()
	now := guideTestNow
	s := testService(t, Options{Now: func() time.Time { return now }})
	n := 450
	if os.Getenv("ETHER_LIVETV_SCALE_TEST") == "1" {
		n = 16000
	}
	const slots = 100
	tx, err := s.o.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ch, err := tx.Prepare(`INSERT INTO livetv_channels(id,data) VALUES(?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	pr, err := tx.Prepare(`INSERT INTO livetv_programmes(source_id,channel_id,start_ms,end_ms,data) VALUES(?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	payloads := make([][]byte, slots)
	for j := range payloads {
		start := now.Add(time.Duration(j-24) * time.Hour)
		payloads[j], _ = json.Marshal(Programme{Start: start, End: start.Add(time.Hour), Item: Item{Title: "Programme", Rating: "G"}})
	}
	ids := make([]string, n)
	for i := range ids {
		id := fmt.Sprintf("scale-%05d", i)
		ids[i] = id
		c := Channel{ID: id, Number: fmt.Sprint(i + 1), Name: id, Kind: "iptv", Enabled: true, SourceID: "scale", GuideID: id}
		b, _ := json.Marshal(c)
		if _, err = ch.Exec(id, b); err != nil {
			t.Fatal(err)
		}
		for j, b := range payloads {
			start := now.Add(time.Duration(j-24) * time.Hour)
			if _, err = pr.Exec("scale", id, start.UnixMilli(), start.Add(time.Hour).UnixMilli(), b); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// Cache warmup is not part of the request's programme read cost.
	if _, err = s.Channels(ctx); err != nil {
		t.Fatal(err)
	}
	statsCtx, stats := store.WithQueryStats(ctx)
	start := time.Now()
	visible, err := s.AllowedChannels(statsCtx, &core.User{ID: 7, MaxContentRating: "G"})
	if err != nil || len(visible) != n {
		t.Fatalf("rated lineup=%d %v", len(visible), err)
	}
	if stats.Rows() > int64(n+5) {
		t.Fatalf("rated rows=%d for %d channels", stats.Rows(), n)
	}
	t.Logf("rated lineup channels=%d programmes=%d returned=%d database_rows=%d queries=%d elapsed=%s", n, n*slots, len(visible), stats.Rows(), stats.Queries(), time.Since(start))
	statsCtx, stats = store.WithQueryStats(ctx)
	start = time.Now()
	ps, err := s.GuideForChannels(statsCtx, ownerUser(), now, now.Add(3*time.Hour), ids[:401])
	if err != nil || len(ps) != 1203 {
		t.Fatalf("guide=%d %v", len(ps), err)
	}
	if stats.Rows() > 1700 {
		t.Fatalf("scoped guide read %d rows", stats.Rows())
	}
	t.Logf("401-channel guide database_rows=%d queries=%d elapsed=%s", stats.Rows(), stats.Queries(), time.Since(start))
	plans := []string{
		`SELECT data FROM livetv_programmes WHERE source_id='scale' AND channel_id='scale-00000' AND start_ms<=0 ORDER BY start_ms DESC LIMIT 1`,
		`SELECT source_id,channel_id,data FROM livetv_programmes WHERE (source_id,channel_id) IN (('scale','scale-00000'),('scale','scale-00001')) AND end_ms>0 AND start_ms<1`,
	}
	// The row and query bounds above hold on every dialect; the plan check
	// reads SQLite's EXPLAIN QUERY PLAN format.
	if store.DialectOf(s.o.DB) != store.SQLite {
		return
	}
	for _, q := range plans {
		rows, err := s.o.DB.QueryContext(ctx, "EXPLAIN QUERY PLAN "+q)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for rows.Next() {
			var a, b, c int
			var detail string
			if err = rows.Scan(&a, &b, &c, &detail); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(detail, "SCAN livetv_programmes") {
				t.Fatal("unbounded scan:", detail)
			}
			if strings.Contains(detail, "SEARCH livetv_programmes") {
				found = true
			}
			t.Log(detail)
		}
		rows.Close()
		if !found {
			t.Fatal("missing indexed programme search")
		}
	}
}
