package newznab

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// newznab_usage breaks newznab_budget's real request counts down by why
// each request was made (core.IndexerPurpose*). It is its own component
// so the newznab migrations upgrade fixtures pin stay untouched. Rows
// follow the budget's retention (Prune).
func init() {
	store.Register("newznab_usage", 1, `
CREATE TABLE newznab_usage (
  indexer_id TEXT NOT NULL,
  day TEXT NOT NULL,
  purpose TEXT NOT NULL,
  hits INTEGER NOT NULL DEFAULT 0,
  grabs INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (indexer_id, day, purpose)
);`)
}

// PurposeUnattributed labels budget hits older than purpose tracking (or
// spent by a node that predates it): the budget counted them, newznab_usage
// did not.
const PurposeUnattributed = "unattributed"

// capsPurpose attributes a capability fetch. The owner's Test button
// fetches caps as part of the test, so that stays a test.
func capsPurpose(ctx context.Context) context.Context {
	if core.IndexerPurpose(ctx) == core.IndexerPurposeTest {
		return ctx
	}
	return core.WithIndexerPurpose(ctx, core.IndexerPurposeCaps)
}

// recordUsage adds one reserved request to the purpose breakdown. It runs
// after the budget accepted the request and never fails it: the breakdown
// is observability only.
func (c *Client) recordUsage(ctx context.Context, kind string) {
	db := c.db
	if db == nil {
		db = c.usageDB
	}
	if db == nil {
		return
	}
	col := "hits"
	if kind == "grab" {
		col = "grabs"
	}
	_, err := db.ExecContext(context.WithoutCancel(ctx),
		`INSERT INTO newznab_usage(indexer_id, day, purpose, `+col+`) VALUES (?,?,?,1)
ON CONFLICT(indexer_id, day, purpose) DO UPDATE SET `+col+`=newznab_usage.`+col+`+1`,
		c.indexerID(), c.today(), usagePurpose(ctx))
	if err != nil {
		c.log.Debug("newznab: record usage", "indexer", c.Name(), "err", err)
	}
}

// TestIndexer is Test, with the requests it makes recorded as manual tests
// in the usage breakdown. Test hits are real requests the indexer counts,
// but they never spend the daily budget.
func (m *Manager) TestIndexer(ctx context.Context, cfg config.Indexer, policy config.ClientIdentity) error {
	return TestWithOptions(core.WithIndexerPurpose(ctx, core.IndexerPurposeTest), cfg, Options{ClientIdentity: policy, usageDB: m.db})
}

// PurposeCount is one purpose's share of a usage window.
type PurposeCount struct {
	Purpose string `json:"purpose"`
	Hits    int64  `json:"hits"`
	Grabs   int64  `json:"grabs"`
}

// UsageWindow totals one indexer's real API hits and grabs over a window.
type UsageWindow struct {
	Hits     int64          `json:"hits"`
	Grabs    int64          `json:"grabs"`
	Purposes []PurposeCount `json:"purposes"`
}

// UsageDay is one UTC day's totals.
type UsageDay struct {
	Day   string `json:"day"`
	Hits  int64  `json:"hits"`
	Grabs int64  `json:"grabs"`
}

// IndexerUsage is one configured indexer's request history: what it would
// show on the indexer's own API-hit counter, by purpose.
type IndexerUsage struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Enabled    bool        `json:"enabled"`
	Torrent    bool        `json:"torrent"`
	DailyLimit int         `json:"dailyLimit"`
	DailyGrabs int         `json:"dailyGrabs"`
	RetryAt    int64       `json:"retryAt,omitempty"`
	Today      UsageWindow `json:"today"`
	Week       UsageWindow `json:"week"`
	Month      UsageWindow `json:"month"`
	Days       []UsageDay  `json:"days"`
}

// UsageReport is every configured indexer's usage. Days are UTC.
type UsageReport struct {
	Today      string         `json:"today"`
	WeekStart  string         `json:"weekStart"`
	MonthStart string         `json:"monthStart"`
	Indexers   []IndexerUsage `json:"indexers"`
}

// usageDays is how many recent days the report lists one by one.
const usageDays = 14

// Usage reports each configured indexer's real API hits and grabs for
// today, the last 7 days and this UTC month, split by purpose. Totals are
// newznab_budget's counts (every request that reached the indexer) plus
// manual tests, which never spend the budget; budget hits the breakdown
// does not explain are reported as PurposeUnattributed.
func (m *Manager) Usage(ctx context.Context) (UsageReport, error) {
	now := time.Now
	if m.opts.Now != nil {
		now = m.opts.Now
	}
	t := now().UTC()
	day := func(d time.Time) string { return d.Format("2006-01-02") }
	rep := UsageReport{
		Today:      day(t),
		WeekStart:  day(t.AddDate(0, 0, -6)),
		MonthStart: day(time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)),
		Indexers:   []IndexerUsage{},
	}
	from := min(rep.WeekStart, rep.MonthStart, day(t.AddDate(0, 0, -(usageDays-1))))

	type key struct{ id, day string }
	budget := map[key][2]int64{}
	purposes := map[key]map[string][2]int64{}
	if m.db != nil {
		if err := scanRows(ctx, m.db, `SELECT indexer_id, day, hits, grabs FROM newznab_budget WHERE day>=?`, from, func(rows *sql.Rows) error {
			var k key
			var v [2]int64
			if err := rows.Scan(&k.id, &k.day, &v[0], &v[1]); err != nil {
				return err
			}
			budget[k] = v
			return nil
		}); err != nil {
			return rep, err
		}
		if err := scanRows(ctx, m.db, `SELECT indexer_id, day, purpose, hits, grabs FROM newznab_usage WHERE day>=?`, from, func(rows *sql.Rows) error {
			var k key
			var p string
			var v [2]int64
			if err := rows.Scan(&k.id, &k.day, &p, &v[0], &v[1]); err != nil {
				return err
			}
			if purposes[k] == nil {
				purposes[k] = map[string][2]int64{}
			}
			purposes[k][p] = v
			return nil
		}); err != nil {
			return rep, err
		}
	}
	// dayPurposes reconciles one indexer-day: tests come on top of the
	// budget; any budget the other purposes do not cover is unattributed.
	dayPurposes := func(k key) map[string][2]int64 {
		out := map[string][2]int64{}
		var covered [2]int64
		for p, v := range purposes[k] {
			// Detail searches have a separate budget class, but belong to
			// the existing opened-details category in the owner's report.
			category := p
			if category == core.IndexerPurposeDetail {
				category = core.IndexerPurposeWarmup
			}
			addCount(out, category, v)
			if p != core.IndexerPurposeTest {
				covered[0] += v[0]
				covered[1] += v[1]
			}
		}
		b := budget[k]
		if rest := [2]int64{max(0, b[0]-covered[0]), max(0, b[1]-covered[1])}; rest != [2]int64{} {
			out[PurposeUnattributed] = rest
		}
		return out
	}

	for _, cl := range m.Clients() {
		cfg := cl.Config()
		u := IndexerUsage{
			ID: cl.indexerID(), Name: cl.Name(), Enabled: cfg.Enabled, Torrent: cfg.IsTorrent(),
			DailyLimit: cfg.DailyLimit, DailyGrabs: cfg.DailyGrabs, Days: []UsageDay{},
		}
		if until, err := cl.rateLimitUntil(ctx); err == nil && cl.now().Before(until) {
			u.RetryAt = until.Unix()
		}
		today, week, month := map[string][2]int64{}, map[string][2]int64{}, map[string][2]int64{}
		for i := usageDays - 1; i >= 0; i-- {
			d := day(t.AddDate(0, 0, -i))
			ud := UsageDay{Day: d}
			for p, v := range dayPurposes(key{u.ID, d}) {
				ud.Hits += v[0]
				ud.Grabs += v[1]
				if d == rep.Today {
					addCount(today, p, v)
				}
				if d >= rep.WeekStart {
					addCount(week, p, v)
				}
				if d >= rep.MonthStart {
					addCount(month, p, v)
				}
			}
			u.Days = append(u.Days, ud)
		}
		// Month days older than the listed window (late in a long month).
		for d := rep.MonthStart; d < day(t.AddDate(0, 0, -(usageDays-1))); {
			for p, v := range dayPurposes(key{u.ID, d}) {
				addCount(month, p, v)
			}
			next, err := time.Parse("2006-01-02", d)
			if err != nil {
				break
			}
			d = day(next.AddDate(0, 0, 1))
		}
		u.Today, u.Week, u.Month = window(today), window(week), window(month)
		rep.Indexers = append(rep.Indexers, u)
	}
	return rep, nil
}

func addCount(m map[string][2]int64, p string, v [2]int64) {
	c := m[p]
	m[p] = [2]int64{c[0] + v[0], c[1] + v[1]}
}

func window(m map[string][2]int64) UsageWindow {
	w := UsageWindow{Purposes: []PurposeCount{}}
	for p, v := range m {
		if v == [2]int64{} {
			continue
		}
		w.Hits += v[0]
		w.Grabs += v[1]
		w.Purposes = append(w.Purposes, PurposeCount{Purpose: p, Hits: v[0], Grabs: v[1]})
	}
	slices.SortFunc(w.Purposes, func(a, b PurposeCount) int {
		return cmp.Or(cmp.Compare(b.Hits, a.Hits), cmp.Compare(b.Grabs, a.Grabs), cmp.Compare(a.Purpose, b.Purpose))
	})
	return w
}

func scanRows(ctx context.Context, db *sql.DB, q string, arg any, each func(*sql.Rows) error) error {
	rows, err := db.QueryContext(ctx, q, arg)
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

// UsageHandler serves Usage as JSON at its root (GET).
func (m *Manager) UsageHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || (r.URL.Path != "/" && r.URL.Path != "") {
			http.NotFound(w, r)
			return
		}
		rep, err := m.Usage(r.Context())
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err != nil {
			m.log.Warn("newznab: usage report", "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "usage_unavailable", "message": "Indexer usage is unavailable"}})
			return
		}
		_ = json.NewEncoder(w).Encode(rep)
	})
}
