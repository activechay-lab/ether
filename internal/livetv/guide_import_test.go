package livetv

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
	// The viewer-policy triggers are what an unchanged import must not fire.
	_ "github.com/braedonsaunders/ether/internal/viewerpolicy"
)

// importLog keeps the counters of the last "live TV source imported" record.
type importLog struct {
	mu    sync.Mutex
	attrs map[string]int64
}

func (h *importLog) Enabled(context.Context, slog.Level) bool { return true }
func (h *importLog) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *importLog) WithGroup(string) slog.Handler            { return h }
func (h *importLog) Handle(_ context.Context, r slog.Record) error {
	if r.Message != "live TV source imported" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.attrs = map[string]int64{}
	r.Attrs(func(a slog.Attr) bool {
		switch a.Value.Kind() {
		case slog.KindInt64:
			h.attrs[a.Key] = a.Value.Int64()
		case slog.KindBool:
			if a.Value.Bool() {
				h.attrs[a.Key] = 1
			}
		}
		return true
	})
	return nil
}

func (h *importLog) last() map[string]int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.attrs
}

// countRowWrites counts every row inserted, updated or deleted in table,
// independently of the import's own counters.
func countRowWrites(t *testing.T, db *sql.DB, table string) func() int64 {
	t.Helper()
	ctx := context.Background()
	stmts := []string{`CREATE TABLE test_row_writes(n BIGINT NOT NULL)`, `INSERT INTO test_row_writes(n) VALUES(0)`}
	if store.DialectOf(db) == store.Postgres {
		stmts = append(stmts,
			`CREATE FUNCTION test_row_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN UPDATE test_row_writes SET n=n+1; RETURN NULL; END $$`,
			`CREATE TRIGGER test_row_writes AFTER INSERT OR UPDATE OR DELETE ON `+table+` FOR EACH ROW EXECUTE FUNCTION test_row_write()`)
	} else {
		for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
			stmts = append(stmts, `CREATE TRIGGER test_row_writes_`+event+` AFTER `+event+` ON `+table+` BEGIN UPDATE test_row_writes SET n=n+1; END`)
		}
	}
	for _, q := range stmts {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	return func() int64 {
		t.Helper()
		var n int64
		if err := db.QueryRowContext(ctx, `SELECT n FROM test_row_writes`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
}

func bigGuide(channels, perChannel int, rating func(ch, i int) string) (string, string) {
	var m3u, xml strings.Builder
	m3u.WriteString("#EXTM3U\n")
	xml.WriteString(`<?xml version="1.0"?><tv>`)
	base := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	for ch := 0; ch < channels; ch++ {
		fmt.Fprintf(&m3u, "#EXTINF:-1 tvg-id=\"ch%d\",Channel %d\nhttps://owner.invalid/live/%d.ts\n", ch, ch, ch)
		fmt.Fprintf(&xml, `<channel id="ch%d"><display-name>Channel %d</display-name></channel>`, ch, ch)
		for i := 0; i < perChannel; i++ {
			start := base.Add(time.Duration(i) * 30 * time.Minute)
			fmt.Fprintf(&xml, `<programme channel="ch%d" start="%s +0000" stop="%s +0000"><title>Show %d</title><rating system="MPAA"><value>%s</value></rating></programme>`,
				ch, start.Format("20060102150405"), start.Add(30*time.Minute).Format("20060102150405"), i, rating(ch, i))
		}
	}
	xml.WriteString(`</tv>`)
	return m3u.String(), xml.String()
}

// Re-importing an unchanged 5,000-programme guide writes no row and leaves
// the viewer-policy epoch alone; changing one programme's rating (which
// restricted viewers' guide and playback depend on) writes exactly that
// programme and does advance the epoch. Runs on PostgreSQL too with
// ETHER_TEST_POSTGRES set.
func TestUnchangedGuideReimportWritesNothing(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	log := &importLog{}
	// The guide is fetched like a provider's, so a refresh that changes only
	// the guide leaves the source definition as it was.
	var served sync.Mutex
	var guide string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served.Lock()
		defer served.Unlock()
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(guide))
	}))
	defer up.Close()
	s := testService(t, Options{DB: db, Logger: slog.New(log), HTTPClient: up.Client()})
	epoch := func() int64 {
		t.Helper()
		var g int64
		if err := db.QueryRowContext(ctx, `SELECT generation FROM viewer_policy_epoch WHERE id=1`).Scan(&g); err != nil {
			t.Fatal(err)
		}
		return g
	}
	rows := countRowWrites(t, db, "livetv_programmes")
	pg := func(int, int) string { return "PG" }
	m3u, first := bigGuide(50, 100, pg)
	guide = first
	src := Source{ID: "big", Name: "Big guide", Playlist: m3u, GuideURL: up.URL + "/guide.xml"}
	if _, err := s.ImportSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	if got := log.last(); got["programmes_written"] != 5000 || got["source_written"] != 1 {
		t.Fatalf("first import: %v", got)
	}

	before, wrote := epoch(), rows()
	started := time.Now()
	if _, err := s.ImportSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	again := log.last()
	t.Logf("unchanged re-import of 5000 programmes in %v: %v, programme row writes %d, epoch advanced %d", time.Since(started), again, rows()-wrote, epoch()-before)
	if rows() != wrote {
		t.Fatalf("unchanged re-import touched %d programme rows", rows()-wrote)
	}
	for _, k := range []string{"programmes_written", "programmes_deleted", "channels_written", "channels_deleted", "guide_channels_written", "guide_channels_deleted", "source_written"} {
		if again[k] != 0 {
			t.Fatalf("unchanged re-import wrote %s=%d: %v", k, again[k], again)
		}
	}
	if again["programmes_unchanged"] != 5000 || epoch() != before {
		t.Fatalf("unchanged re-import: %v, epoch %d -> %d", again, before, epoch())
	}

	_, changed := bigGuide(50, 100, func(ch, i int) string {
		if ch == 7 && i == 42 {
			return "R"
		}
		return "PG"
	})
	served.Lock()
	guide = changed
	served.Unlock()
	before, wrote = epoch(), rows()
	if _, err := s.ImportSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	rated := log.last()
	t.Logf("one changed rating: %v, programme row writes %d, epoch advanced %d", rated, rows()-wrote, epoch()-before)
	if rated["programmes_written"] != 1 || rated["programmes_deleted"] != 0 || rated["source_written"] != 0 || rows()-wrote != 1 || epoch() <= before {
		t.Fatalf("a changed rating: %v, epoch %d -> %d", rated, before, epoch())
	}
	var data string
	start := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC).Add(42 * 30 * time.Minute).UnixMilli()
	if err := db.QueryRowContext(ctx, `SELECT data FROM livetv_programmes WHERE source_id=? AND channel_id=? AND start_ms=?`, "big", "ch7", start).Scan(&data); err != nil || !strings.Contains(data, `"R"`) {
		t.Fatalf("stored rating: %v %s", err, data)
	}
}
