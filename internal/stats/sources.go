package stats

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// Sources › Statistics reads daily history for every source type. Usenet
// providers and indexers already keep daily rows; debrid accounts and the
// debrid streaming engine only kept counters since start, so the stats
// sampler (every 30 s, never on a request path) folds their deltas into
// daily rows here, exactly like stats_provider_days.
func init() {
	store.Register("stats", 11, `CREATE TABLE stats_debrid_days(provider TEXT NOT NULL,day TEXT NOT NULL,requests INTEGER NOT NULL DEFAULT 0,failures INTEGER NOT NULL DEFAULT 0,cached INTEGER NOT NULL DEFAULT 0,not_cached INTEGER NOT NULL DEFAULT 0,blocked INTEGER NOT NULL DEFAULT 0,links_refreshed INTEGER NOT NULL DEFAULT 0,reused INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(provider,day));
CREATE TABLE stats_stream_days(day TEXT PRIMARY KEY,memory_hits INTEGER NOT NULL DEFAULT 0,disk_hits INTEGER NOT NULL DEFAULT 0,blocks_fetched INTEGER NOT NULL DEFAULT 0,bytes_fetched INTEGER NOT NULL DEFAULT 0,hedges INTEGER NOT NULL DEFAULT 0,hedge_wins INTEGER NOT NULL DEFAULT 0,link_refreshes INTEGER NOT NULL DEFAULT 0,errors INTEGER NOT NULL DEFAULT 0);`)
}

// maxSourceDays bounds the Statistics window.
const maxSourceDays = 90

// DebridSample is one debrid account's cumulative counters on this node.
type DebridSample struct {
	ID                                                                     string
	Requests, Failures, Cached, NotCached, Blocked, LinksRefreshed, Reused int64
}

// StreamSample is the debrid streaming engine's cumulative counters on
// this node: block cache hits against blocks fetched from the CDN.
type StreamSample struct {
	MemoryHits, DiskHits, BlocksFetched, BytesFetched, Hedges, HedgeWins, LinkRefreshes, Errors int64
}

func counterDelta(n, o int64) int64 {
	if n < o { // the counter restarted
		return n
	}
	return n - o
}

// SnapshotDebrid adds each account's counter growth since the last sample
// to today's row. Unchanged accounts write nothing.
func (s *Service) SnapshotDebrid(ctx context.Context, ps []DebridSample) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prevDebrid == nil {
		s.prevDebrid = map[string]DebridSample{}
	}
	day := s.opts.Now().UTC().Format("2006-01-02")
	for _, p := range ps {
		o := s.prevDebrid[p.ID]
		d := []int64{counterDelta(p.Requests, o.Requests), counterDelta(p.Failures, o.Failures), counterDelta(p.Cached, o.Cached), counterDelta(p.NotCached, o.NotCached), counterDelta(p.Blocked, o.Blocked), counterDelta(p.LinksRefreshed, o.LinksRefreshed), counterDelta(p.Reused, o.Reused)}
		s.prevDebrid[p.ID] = p
		if allZero(d) {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO stats_debrid_days(provider,day,requests,failures,cached,not_cached,blocked,links_refreshed,reused) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(provider,day) DO UPDATE SET requests=stats_debrid_days.requests+excluded.requests,failures=stats_debrid_days.failures+excluded.failures,cached=stats_debrid_days.cached+excluded.cached,not_cached=stats_debrid_days.not_cached+excluded.not_cached,blocked=stats_debrid_days.blocked+excluded.blocked,links_refreshed=stats_debrid_days.links_refreshed+excluded.links_refreshed,reused=stats_debrid_days.reused+excluded.reused`, p.ID, day, d[0], d[1], d[2], d[3], d[4], d[5], d[6]); err != nil {
			return err
		}
	}
	return nil
}

// SnapshotStream adds the engine's counter growth since the last sample to
// today's row.
func (s *Service) SnapshotStream(ctx context.Context, p StreamSample) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.prevStream
	d := []int64{counterDelta(p.MemoryHits, o.MemoryHits), counterDelta(p.DiskHits, o.DiskHits), counterDelta(p.BlocksFetched, o.BlocksFetched), counterDelta(p.BytesFetched, o.BytesFetched), counterDelta(p.Hedges, o.Hedges), counterDelta(p.HedgeWins, o.HedgeWins), counterDelta(p.LinkRefreshes, o.LinkRefreshes), counterDelta(p.Errors, o.Errors)}
	s.prevStream = p
	if allZero(d) {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO stats_stream_days(day,memory_hits,disk_hits,blocks_fetched,bytes_fetched,hedges,hedge_wins,link_refreshes,errors) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(day) DO UPDATE SET memory_hits=stats_stream_days.memory_hits+excluded.memory_hits,disk_hits=stats_stream_days.disk_hits+excluded.disk_hits,blocks_fetched=stats_stream_days.blocks_fetched+excluded.blocks_fetched,bytes_fetched=stats_stream_days.bytes_fetched+excluded.bytes_fetched,hedges=stats_stream_days.hedges+excluded.hedges,hedge_wins=stats_stream_days.hedge_wins+excluded.hedge_wins,link_refreshes=stats_stream_days.link_refreshes+excluded.link_refreshes,errors=stats_stream_days.errors+excluded.errors`, s.opts.Now().UTC().Format("2006-01-02"), d[0], d[1], d[2], d[3], d[4], d[5], d[6], d[7])
	return err
}

func allZero(v []int64) bool {
	for _, n := range v {
		if n != 0 {
			return false
		}
	}
	return true
}

// SourceHistory is the Statistics document: daily rows per source over a
// bounded window of UTC days (since..until inclusive).
type SourceHistory struct {
	Days         int                 `json:"days"`
	Since        string              `json:"since"`
	Until        string              `json:"until"`
	Plays        []SourcePlayDay     `json:"plays"`
	ProviderDays []SourceProviderDay `json:"providerDays"`
	IndexerDays  []SourceIndexerDay  `json:"indexerDays"`
	DebridDays   []SourceDebridDay   `json:"debridDays"`
	StreamDays   []SourceStreamDay   `json:"streamDays"`
}

// SourcePlayDay counts plays started on a UTC day by where they streamed from.
type SourcePlayDay struct {
	Day       string `json:"day"`
	Origin    string `json:"origin"`
	Plays     int64  `json:"plays"`
	Failovers int64  `json:"failovers"`
	Failed    int64  `json:"failed"`
}

type SourceProviderDay struct {
	ID            string `json:"id"`
	Day           string `json:"day"`
	Bytes         int64  `json:"bytes"`
	Articles      int64  `json:"articles"`
	Misses        int64  `json:"misses"`
	Errors        int64  `json:"errors"`
	BackfillSaves int64  `json:"backfillSaves"`
	BodyMisses    int64  `json:"bodyMisses"`
	BodyTakedowns int64  `json:"bodyTakedowns"`
	Spillovers    int64  `json:"spillovers"`
}

type SourceIndexerDay struct {
	Indexer     string `json:"indexer"`
	Day         string `json:"day"`
	Searches    int64  `json:"searches"`
	Successes   int64  `json:"successes"`
	Grabs       int64  `json:"grabs"`
	GrabSuccess int64  `json:"grabSuccess"`
	Budgets     int64  `json:"budgets"`
	LatencyMS   int64  `json:"latencyMs"`
	AgeSeconds  int64  `json:"ageSeconds"`
	AgeSamples  int64  `json:"ageSamples"`
}

type SourceDebridDay struct {
	ID             string `json:"id"`
	Day            string `json:"day"`
	Requests       int64  `json:"requests"`
	Failures       int64  `json:"failures"`
	Cached         int64  `json:"cached"`
	NotCached      int64  `json:"notCached"`
	Blocked        int64  `json:"blocked"`
	LinksRefreshed int64  `json:"linksRefreshed"`
	Reused         int64  `json:"reused"`
}

type SourceStreamDay struct {
	Day           string `json:"day"`
	MemoryHits    int64  `json:"memoryHits"`
	DiskHits      int64  `json:"diskHits"`
	BlocksFetched int64  `json:"blocksFetched"`
	BytesFetched  int64  `json:"bytesFetched"`
	Hedges        int64  `json:"hedges"`
	HedgeWins     int64  `json:"hedgeWins"`
	LinkRefreshes int64  `json:"linkRefreshes"`
	Errors        int64  `json:"errors"`
}

// SourceHistoryFor reads the last `days` UTC days (today included). Every
// query is bounded by the window: daily tables by their (source, day)
// keys, plays by the started index.
func (s *Service) SourceHistoryFor(ctx context.Context, days int) (SourceHistory, error) {
	if days < 1 {
		days = 30
	}
	if days > maxSourceDays {
		days = maxSourceDays
	}
	today := s.opts.Now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -(days - 1))
	since := start.Format("2006-01-02")
	out := SourceHistory{Days: days, Since: since, Until: today.Format("2006-01-02"),
		Plays: []SourcePlayDay{}, ProviderDays: []SourceProviderDay{}, IndexerDays: []SourceIndexerDay{}, DebridDays: []SourceDebridDay{}, StreamDays: []SourceStreamDay{}}

	if err := scanRows(ctx, s.db, `SELECT started/86400,CASE WHEN origin='' THEN 'Other' ELSE origin END,COUNT(*),CAST(COALESCE(SUM(failovers),0) AS BIGINT),CAST(COALESCE(SUM(CASE WHEN error<>'' THEN 1 ELSE 0 END),0) AS BIGINT) FROM stats_plays WHERE started>=? GROUP BY 1,2 ORDER BY 1,2`, []any{start.Unix()}, func(r *sql.Rows) error {
		var d SourcePlayDay
		var n int64
		if err := r.Scan(&n, &d.Origin, &d.Plays, &d.Failovers, &d.Failed); err != nil {
			return err
		}
		d.Day = time.Unix(n*86400, 0).UTC().Format("2006-01-02")
		out.Plays = append(out.Plays, d)
		return nil
	}); err != nil {
		return out, err
	}
	if err := scanRows(ctx, s.db, `SELECT provider,day,bytes,articles,misses,errors,backfill_saves,body_misses,body_takedowns,spillovers FROM stats_provider_days WHERE day>=? ORDER BY day,provider`, []any{since}, func(r *sql.Rows) error {
		var d SourceProviderDay
		if err := r.Scan(&d.ID, &d.Day, &d.Bytes, &d.Articles, &d.Misses, &d.Errors, &d.BackfillSaves, &d.BodyMisses, &d.BodyTakedowns, &d.Spillovers); err != nil {
			return err
		}
		out.ProviderDays = append(out.ProviderDays, d)
		return nil
	}); err != nil {
		return out, err
	}
	if err := scanRows(ctx, s.db, `SELECT indexer,day,searches,successes,grabs,grab_success,budgets,latency_ms,age_seconds,age_samples FROM stats_indexer_days WHERE day>=? ORDER BY day,indexer`, []any{since}, func(r *sql.Rows) error {
		var d SourceIndexerDay
		if err := r.Scan(&d.Indexer, &d.Day, &d.Searches, &d.Successes, &d.Grabs, &d.GrabSuccess, &d.Budgets, &d.LatencyMS, &d.AgeSeconds, &d.AgeSamples); err != nil {
			return err
		}
		out.IndexerDays = append(out.IndexerDays, d)
		return nil
	}); err != nil {
		return out, err
	}
	if err := scanRows(ctx, s.db, `SELECT provider,day,requests,failures,cached,not_cached,blocked,links_refreshed,reused FROM stats_debrid_days WHERE day>=? ORDER BY day,provider`, []any{since}, func(r *sql.Rows) error {
		var d SourceDebridDay
		if err := r.Scan(&d.ID, &d.Day, &d.Requests, &d.Failures, &d.Cached, &d.NotCached, &d.Blocked, &d.LinksRefreshed, &d.Reused); err != nil {
			return err
		}
		out.DebridDays = append(out.DebridDays, d)
		return nil
	}); err != nil {
		return out, err
	}
	err := scanRows(ctx, s.db, `SELECT day,memory_hits,disk_hits,blocks_fetched,bytes_fetched,hedges,hedge_wins,link_refreshes,errors FROM stats_stream_days WHERE day>=? ORDER BY day`, []any{since}, func(r *sql.Rows) error {
		var d SourceStreamDay
		if err := r.Scan(&d.Day, &d.MemoryHits, &d.DiskHits, &d.BlocksFetched, &d.BytesFetched, &d.Hedges, &d.HedgeWins, &d.LinkRefreshes, &d.Errors); err != nil {
			return err
		}
		out.StreamDays = append(out.StreamDays, d)
		return nil
	})
	return out, err
}

func scanRows(ctx context.Context, db *sql.DB, q string, args []any, each func(*sql.Rows) error) error {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := each(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Service) sourceHistory(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	v, err := s.SourceHistoryFor(r.Context(), days)
	if err != nil {
		fail(w, 500, "Source history unavailable")
		return
	}
	reply(w, 200, v)
}
