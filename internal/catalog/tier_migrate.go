package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// The tier migration shrinks a catalogue that mirrored TMDB's long tail as
// full rows (the Off preset before 0.3.6, about 800k titles) to the library
// tier: rows that meet the bars, rows a viewer or the owner touched, and
// what those rows need. Everything else is TMDB's long tail, searchable
// from the export index and stored again by catalog.Get when opened.
//
// It is not a schema migration: those run in one transaction, which on
// PostgreSQL would hold catalog_titles' locks for the whole delete and on
// SQLite would block every writer and grow the WAL by gigabytes. The
// migration below only arms it, on a database that already has titles. The
// job runs once, beside the server, after the upgrade:
//
//  1. It builds the keep set: the stored rows' own facts (bars, detail,
//     franchise) and every title the touch sources name (tier_sources.go).
//  2. It waits for a backup taken since the upgrade (TierBackupFunc takes
//     one when none exists). Nothing is removed before.
//  3. It removes the rest. On PostgreSQL in key order, a show with its
//     seasons and episodes, a batch a transaction (row locks only); each
//     batch re-reads its titles' stored facts and the Tier A sources for
//     its key range in the same transaction, so a title touched while the
//     job ran is kept, and triggers keep the search index and the tallies
//     right row by row. On SQLite it copies the keepers into a fresh table
//     and swaps it in (tier_swap.go), with the same per-batch re-check. A
//     cursor in catalog_meta resumes either after a restart.
//  4. It re-reads every touch source and stores again, through Get, any
//     touched title that has no row (a PostgreSQL touch committed between a
//     batch's re-check and its delete, or a scanned source edited during
//     the run).
//
// The owner can pause and resume it from the catalogue status
// (SetTierMigrationPaused). It never runs again once done: raising the
// bars later demotes nothing.
func init() {
	store.RegisterAfter("catalog_tier_migrate", "catalog")
	// Armed only on a database that already holds titles: a new install
	// (and every test database) has nothing to shrink.
	store.Register("catalog_tier_migrate", 1, `INSERT INTO catalog_meta(k,v)
SELECT 'tier_migrate_armed', CAST(CAST(strftime('%s','now') AS INTEGER) AS TEXT)
WHERE EXISTS (SELECT 1 FROM catalog_titles) ON CONFLICT(k) DO NOTHING;`)
	store.RegisterDialect("catalog_tier_migrate", 1, store.Postgres, `INSERT INTO catalog_meta(k,v)
SELECT 'tier_migrate_armed', CAST(CAST(EXTRACT(EPOCH FROM now()) AS BIGINT) AS TEXT)
WHERE EXISTS (SELECT 1 FROM catalog_titles) ON CONFLICT(k) DO NOTHING;`)
}

const (
	tierMetaArmed    = "tier_migrate_armed"
	tierMetaDone     = "tier_migrate_done"
	tierMetaPaused   = "tier_migrate_paused"
	tierMetaCursor   = "tier_migrate_cursor"
	tierMetaBackup   = "tier_migrate_backup"
	tierMetaProgress = "tier_migrate_progress"
)

const (
	// tierPlanPage is how many rows one planning read returns.
	tierPlanPage = 5000
	// tierBatchMax bounds the rows one removal transaction deletes; the
	// hold-time rule (nextBatch) shrinks it on a slow disk.
	tierBatchMax   = 2000
	tierBatchStart = 500
	// tierRetry spaces attempts after a failure (a backup that could not be
	// taken, a database error).
	tierRetry = 5 * time.Minute
	// tierPausePoll is how often a paused job re-reads the flag another
	// node may have cleared.
	tierPausePoll = 5 * time.Second
	// tierGenEvery bounds how often removals drop the read caches: every
	// batch would make every cached page miss for the whole run.
	tierGenEvery = 5 * time.Second
	// tierSweepRate bounds the re-promotion sweep's Get calls a second.
	tierSweepRate = 4
	// tierSweepMax bounds the titles one sweep re-promotes.
	tierSweepMax = 5000
)

// TierBackupFunc returns the name of a backup (or pre-upgrade snapshot)
// taken at or after since, taking one when there is none. The tier
// migration removes nothing until it returns without an error.
type TierBackupFunc func(ctx context.Context, since time.Time) (string, error)

// TierMigration is the tier migration's progress, shown in the catalogue
// status. It is stored in catalog_meta so every cluster node reports it.
type TierMigration struct {
	// State is "" (never armed: nothing to migrate), "pending",
	// "planning", "waiting_backup", "removing", "paused", "sweeping",
	// "done" or "error".
	State  string `json:"state,omitempty"`
	Paused bool   `json:"paused,omitempty"`
	// Touched is how many titles the touch sources name.
	Touched int `json:"touched,omitempty"`
	// Scanned rows were read by the plan; Keep of them stay and Remove
	// are to be removed (in this run, after the cursor).
	Scanned int `json:"scanned,omitempty"`
	Keep    int `json:"keep,omitempty"`
	Remove  int `json:"remove,omitempty"`
	// Removed rows are gone. Rescued were planned for removal and kept by
	// a batch's re-check. Promoted were stored again by the final sweep.
	Removed    int       `json:"removed,omitempty"`
	Rescued    int       `json:"rescued,omitempty"`
	Promoted   int       `json:"promoted,omitempty"`
	Backup     string    `json:"backup,omitempty"`
	StartedAt  time.Time `json:"startedAt,omitzero"`
	FinishedAt time.Time `json:"finishedAt,omitzero"`
	LastError  string    `json:"lastError,omitempty"`
}

type tierMigrationState struct {
	running atomic.Bool
	// hintsOff leaves INDEXED BY pins out while the swap rebuilds the
	// pinned indexes (indexedBy).
	hintsOff atomic.Bool
	mu       sync.Mutex
	backup   TierBackupFunc
	done     func()
	wake     chan struct{}
}

// SetTierBackup wires the backup check the tier migration waits for.
func (c *Catalog) SetTierBackup(fn TierBackupFunc) {
	c.tier.mu.Lock()
	c.tier.backup = fn
	c.tier.mu.Unlock()
	c.wakeTier()
}

// OnTierMigrated registers fn to run once the tier migration finishes (the
// tail index rebuilds without the removed rows' exclusion).
func (c *Catalog) OnTierMigrated(fn func()) {
	c.tier.mu.Lock()
	c.tier.done = fn
	c.tier.mu.Unlock()
}

func (c *Catalog) tierBackupFunc() TierBackupFunc {
	c.tier.mu.Lock()
	defer c.tier.mu.Unlock()
	return c.tier.backup
}

func (c *Catalog) wakeTier() {
	c.tier.mu.Lock()
	if c.tier.wake == nil {
		c.tier.wake = make(chan struct{}, 1)
	}
	ch := c.tier.wake
	c.tier.mu.Unlock()
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (c *Catalog) tierWake() <-chan struct{} {
	c.tier.mu.Lock()
	defer c.tier.mu.Unlock()
	if c.tier.wake == nil {
		c.tier.wake = make(chan struct{}, 1)
	}
	return c.tier.wake
}

// TierMigrationDone reports whether nothing is left for the tier migration
// (it finished, or the database never needed it).
func (c *Catalog) TierMigrationDone(ctx context.Context) bool {
	return c.getMeta(ctx, tierMetaArmed) == "" || c.getMeta(ctx, tierMetaDone) != ""
}

// SetTierMigrationPaused pauses or resumes the tier migration. It applies
// on every node: the flag is in catalog_meta.
func (c *Catalog) SetTierMigrationPaused(ctx context.Context, paused bool) error {
	if c.getMeta(ctx, tierMetaArmed) == "" {
		return errors.New("catalog: there is no tier migration to pause")
	}
	v := ""
	if paused {
		v = "1"
	}
	if err := c.setMeta(ctx, tierMetaPaused, v); err != nil {
		return err
	}
	c.log.Info("catalog: tier migration "+map[bool]string{true: "paused", false: "resumed"}[paused], "by", "owner")
	c.wakeTier()
	return nil
}

// TierMigrationStatus is the stored progress, with the pause flag.
func (c *Catalog) TierMigrationStatus(ctx context.Context) TierMigration {
	var p TierMigration
	if c.getMeta(ctx, tierMetaArmed) == "" {
		return p
	}
	if raw := c.getMeta(ctx, tierMetaProgress); raw != "" {
		_ = json.Unmarshal([]byte(raw), &p)
	}
	if p.State == "" {
		p.State = "pending"
	}
	p.Paused = c.getMeta(ctx, tierMetaPaused) != ""
	if p.Paused && p.State != "done" {
		p.State = "paused"
	}
	return p
}

func (c *Catalog) saveTierProgress(ctx context.Context, q execer, p TierMigration) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO catalog_meta(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, tierMetaProgress, string(raw))
	return err
}

// runTierMigration runs the job until it is done or ctx ends, retrying
// after failures.
func (c *Catalog) runTierMigration(ctx context.Context) {
	if c == nil || !c.tier.running.CompareAndSwap(false, true) {
		return
	}
	defer c.tier.running.Store(false)
	for ctx.Err() == nil {
		err := c.migrateTiers(ctx)
		if err == nil || ctx.Err() != nil {
			return
		}
		c.log.Error("catalog: tier migration", "err", err, "retry_after", tierRetry)
		p := c.TierMigrationStatus(ctx)
		p.LastError = err.Error()
		if !errors.Is(err, errTierWaitingBackup) {
			p.State = "error"
		}
		_ = c.saveTierProgress(ctx, c.db, p)
		if !c.waitTier(ctx, tierRetry) {
			return
		}
	}
}

// waitTier waits d, or until the job is woken (resumed, backup wired).
func (c *Catalog) waitTier(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-c.tierWake():
		return true
	case <-timer.C:
		return true
	}
}

var errTierWaitingBackup = errors.New("catalog: tier migration waiting for a backup")

// waitTierUnpaused blocks while the owner has the job paused. It reports
// false when ctx ends.
func (c *Catalog) waitTierUnpaused(ctx context.Context, p *TierMigration) bool {
	announced := false
	for c.getMeta(ctx, tierMetaPaused) != "" {
		if ctx.Err() != nil {
			return false
		}
		if !announced {
			p.Paused = true
			_ = c.saveTierProgress(ctx, c.db, *p)
			announced = true
		}
		if !c.waitTier(ctx, tierPausePoll) {
			return false
		}
	}
	if announced {
		p.Paused = false
		_ = c.saveTierProgress(ctx, c.db, *p)
	}
	return ctx.Err() == nil
}

func (c *Catalog) migrateTiers(ctx context.Context) error {
	if c.getMeta(ctx, tierMetaDone) != "" {
		return nil
	}
	armedRaw := c.getMeta(ctx, tierMetaArmed)
	if armedRaw == "" {
		return nil
	}
	armedUnix, _ := strconv.ParseInt(armedRaw, 10, 64)
	armed := time.Unix(armedUnix, 0).UTC()
	p := c.TierMigrationStatus(ctx)
	p.LastError = ""
	if p.StartedAt.IsZero() {
		p.StartedAt = c.now().UTC()
	}
	if !c.waitTierUnpaused(ctx, &p) {
		return ctx.Err()
	}
	p.State = "planning"
	if err := c.saveTierProgress(ctx, c.db, p); err != nil {
		return err
	}
	started := time.Now()
	touched, perSource, err := c.collectTouched(ctx)
	if err != nil {
		return err
	}
	cursor := core.RatingKey(0)
	if v := c.getMeta(ctx, tierMetaCursor); v != "" {
		n, _ := strconv.ParseInt(v, 10, 64)
		cursor = core.RatingKey(n)
	}
	plan, err := c.planTierRemoval(ctx, touched, cursor)
	if err != nil {
		return err
	}
	p.Touched, p.Scanned, p.Keep, p.Remove = len(touched.keys), plan.scanned, plan.keep, plan.rows
	c.log.Info("catalog: tier migration plan", "touched_titles", len(touched.keys), "touched_collections", len(touched.collections),
		"rows_scanned", plan.scanned, "keep", plan.keep, "remove", plan.rows, "remove_groups", len(plan.groups),
		"resume_after", int64(cursor), "took", time.Since(started).Round(time.Millisecond), "sources", perSource)
	if plan.rows > 0 && c.getMeta(ctx, tierMetaBackup) == "" {
		p.State = "waiting_backup"
		_ = c.saveTierProgress(ctx, c.db, p)
		fn := c.tierBackupFunc()
		if fn == nil {
			return fmt.Errorf("%w: no backup service", errTierWaitingBackup)
		}
		name, err := fn(ctx, armed)
		if err != nil {
			return fmt.Errorf("%w: %v", errTierWaitingBackup, err)
		}
		if name == "" {
			return fmt.Errorf("%w: none found", errTierWaitingBackup)
		}
		if err := c.setMeta(ctx, tierMetaBackup, name); err != nil {
			return err
		}
		c.log.Info("catalog: tier migration backup in place", "backup", name, "upgrade", armed)
	}
	p.Backup = c.getMeta(ctx, tierMetaBackup)
	p.State = "removing"
	if err := c.saveTierProgress(ctx, c.db, p); err != nil {
		return err
	}
	if store.DialectOf(c.db) == store.SQLite {
		// Deleting from a table with two dozen indexes and an FTS index
		// rewrites the same pages batch after batch; copying the keepers
		// writes only them (tier_swap.go).
		if err := c.swapTiers(ctx, touched, &p); err != nil {
			return err
		}
	} else if err := c.removeTierGroups(ctx, plan.groups, touched, &p); err != nil {
		return err
	}
	p.State = "sweeping"
	_ = c.saveTierProgress(ctx, c.db, p)
	promoted, err := c.sweepTierTouched(ctx)
	if err != nil {
		return err
	}
	p.Promoted += promoted
	p.State, p.FinishedAt, p.Paused = "done", c.now().UTC(), false
	if err := c.saveTierProgress(ctx, c.db, p); err != nil {
		return err
	}
	if err := c.setMeta(ctx, tierMetaDone, strconv.FormatInt(c.now().Unix(), 10)); err != nil {
		return err
	}
	c.writeGen.Add(1)
	c.log.Info("catalog: tier migration done", "removed", p.Removed, "rescued", p.Rescued, "promoted", p.Promoted,
		"kept", p.Keep, "took", p.FinishedAt.Sub(p.StartedAt).Round(time.Second))
	c.tier.mu.Lock()
	done := c.tier.done
	c.tier.mu.Unlock()
	if done != nil {
		done()
	}
	return nil
}

// tierBars are the bars the library tier keeps on stored values: the
// preset's, or the Off floor when the preset hides nothing.
func tierBars(cfg config.Catalog) (minPop float64, minVotes int) { return cfg.Bars() }

// tierRow is what the keep rule reads of a stored row.
type tierRow struct {
	key          core.RatingKey
	typ          string
	showKey      core.RatingKey
	popularity   float64
	votes        int
	detail       bool
	collectionID int64
	releasedTS   int64
}

// tierKeepsOwnFacts reports whether a movie or show row stays on its own
// stored facts: it meets the bars (released or not: an upcoming title with
// an audience is library material), it is in the new-release window with
// traction, it was opened (detail), or it is a franchise part.
func (c *Catalog) tierKeepsOwnFacts(r tierRow, minPop float64, minVotes int, now time.Time, touched *tierTouched) bool {
	if r.detail || r.collectionID != 0 {
		return true
	}
	if touched.has(r.key) {
		return true
	}
	if (minPop > 0 && r.popularity >= minPop) || (minVotes > 0 && r.votes >= minVotes) {
		return true
	}
	if r.releasedTS != 0 {
		rel := time.Unix(r.releasedTS, 0)
		if !rel.After(now) && now.Sub(rel) <= 45*24*time.Hour {
			if r.popularity >= math.Max(2.5, minPop*0.6) && r.votes >= max(10, minVotes/5) {
				return true
			}
		}
	}
	return false
}

// tierGroup is one removal unit: a movie, or a show with its seasons and
// episodes (show first: deleting a show's episodes after the show skips
// the per-episode show_added_ts recompute).
type tierGroup struct {
	head core.RatingKey
	keys []core.RatingKey
}

type tierPlan struct {
	groups              []tierGroup
	scanned, keep, rows int
}

// planTierRemoval reads catalog_titles in key order, a page at a time, and
// returns the groups after cursor to remove.
func (c *Catalog) planTierRemoval(ctx context.Context, touched *tierTouched, cursor core.RatingKey) (tierPlan, error) {
	var plan tierPlan
	minPop, minVotes := tierBars(c.config())
	now := c.now()
	removedShow := map[core.RatingKey]int{} // show key -> index in plan.groups
	var after int64
	for {
		rows, err := c.db.QueryContext(ctx, `SELECT key,type,show_key,popularity,vote_count,detail,collection_id,released_ts FROM catalog_titles WHERE key>? ORDER BY key LIMIT ?`, after, tierPlanPage)
		if err != nil {
			return plan, err
		}
		n := 0
		for rows.Next() {
			var r tierRow
			var key, show int64
			var detail int
			if err := rows.Scan(&key, &r.typ, &show, &r.popularity, &r.votes, &detail, &r.collectionID, &r.releasedTS); err != nil {
				rows.Close()
				return plan, err
			}
			n++
			after = key
			r.key, r.showKey, r.detail = core.RatingKey(key), core.RatingKey(show), detail != 0
			plan.scanned++
			switch r.typ {
			case string(core.TypeMovie), string(core.TypeShow):
				if c.tierKeepsOwnFacts(r, minPop, minVotes, now, touched) {
					plan.keep++
					continue
				}
				if r.key <= cursor {
					// Planned and kept by an earlier run's re-check.
					plan.keep++
					continue
				}
				if r.typ == string(core.TypeShow) {
					removedShow[r.key] = len(plan.groups)
				}
				plan.groups = append(plan.groups, tierGroup{head: r.key, keys: []core.RatingKey{r.key}})
				plan.rows++
			case string(core.TypeSeason), string(core.TypeEpisode):
				show := r.showKey
				if show == 0 {
					show = core.ShowKey(r.key.TMDB())
				}
				if i, ok := removedShow[show]; ok {
					plan.groups[i].keys = append(plan.groups[i].keys, r.key)
					plan.rows++
					continue
				}
				plan.keep++
			default:
				plan.keep++
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return plan, err
		}
		if n < tierPlanPage {
			break
		}
		if err := ctx.Err(); err != nil {
			return plan, err
		}
	}
	return plan, nil
}

// tierDependents are the per-title tables removed with a title, each by an
// indexed key column. Tables keyed by a rating key whose rows are caches
// the next open rebuilds are left: an orphan there is harmless.
var tierDependents = []struct{ table, column string }{
	{"catalog_genres", "key"},
	{"catalog_countries", "key"},
	{"catalog_studios", "key"},
	{"catalog_keywords", "key"},
	{"catalog_credits", "key"},
	{"catalog_alt_titles", "key"},
	{"catalog_crowd", "key"},
	{"catalog_home_release", "key"},
	{"catalog_home_release_dates", "key"},
	{"catalog_remote_trailers", "key"},
	{"catalog_release_names", "key"},
	{"catalog_release_evidence", "rating_key"},
	{"catalog_show_episodes", "show_key"},
	{"catalog_show_episode_ratings", "show_key"},
	{"plexmeta_titles", "rating_key"},
	{"plex_ultrablur", "key"},
}

// removeTierGroups deletes the planned groups in order.
func (c *Catalog) removeTierGroups(ctx context.Context, groups []tierGroup, touched *tierTouched, p *TierMigration) error {
	tables, err := existingTables(ctx, c.db, store.DialectOf(c.db))
	if err != nil {
		return err
	}
	limit := tierBatchStart
	lastGen := time.Now()
	for start := 0; start < len(groups); {
		if !c.waitTierUnpaused(ctx, p) {
			return ctx.Err()
		}
		if err := store.WaitWALBudget(ctx, c.db); err != nil {
			return err
		}
		end, rows := start, 0
		for end < len(groups) && (rows == 0 || rows+len(groups[end].keys) <= limit) {
			rows += len(groups[end].keys)
			end++
		}
		began := time.Now()
		removed, _, err := c.removeTierBatch(ctx, tables, groups[start:end], touched, p)
		if err != nil {
			return err
		}
		took := time.Since(began)
		start = end
		if time.Since(lastGen) >= tierGenEvery || start == len(groups) {
			c.writeGen.Add(1)
			lastGen = time.Now()
		}
		limit = nextBatch(limit, max(removed, 1), took, tierBatchMax)
	}
	return nil
}

// removeTierBatch removes one batch in one transaction: it re-reads the
// heads' stored facts and the Tier A sources for the batch's key ranges,
// drops every group either keeps, deletes the rest with their dependent
// rows, and moves the cursor.
func (c *Catalog) removeTierBatch(ctx context.Context, tables map[string]bool, batch []tierGroup, touched *tierTouched, p *TierMigration) (removed, rescued int, err error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	minPop, minVotes := tierBars(c.config())
	now := c.now()
	heads := make([]any, len(batch))
	for i, g := range batch {
		heads[i] = int64(g.head)
	}
	current := map[core.RatingKey]tierRow{}
	rows, err := tx.QueryContext(ctx, `SELECT key,type,popularity,vote_count,detail,collection_id,released_ts FROM catalog_titles WHERE key IN (`+placeholders(len(heads))+`)`, heads...)
	if err != nil {
		return 0, 0, err
	}
	for rows.Next() {
		var r tierRow
		var key int64
		var detail int
		if err := rows.Scan(&key, &r.typ, &r.popularity, &r.votes, &detail, &r.collectionID, &r.releasedTS); err != nil {
			rows.Close()
			return 0, 0, err
		}
		r.key, r.detail = core.RatingKey(key), detail != 0
		current[r.key] = r
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if err := recheckTierA(ctx, tx, tables, tierBatchRanges(batch), touched); err != nil {
		return 0, 0, err
	}
	var keys []any
	for _, g := range batch {
		r, ok := current[g.head]
		if ok && c.tierKeepsOwnFacts(r, minPop, minVotes, now, touched) {
			rescued += len(g.keys)
			continue
		}
		for _, k := range g.keys {
			keys = append(keys, int64(k))
		}
	}
	for start := 0; start < len(keys); start += 500 {
		chunk := keys[start:min(start+500, len(keys))]
		in := placeholders(len(chunk))
		res, err := tx.ExecContext(ctx, `DELETE FROM catalog_titles WHERE key IN (`+in+`)`, chunk...)
		if err != nil {
			return 0, 0, fmt.Errorf("catalog: tier migration: delete titles: %w", err)
		}
		n, _ := res.RowsAffected()
		removed += int(n)
		for _, d := range tierDependents {
			if !tables[d.table] {
				continue
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+d.table+` WHERE `+d.column+` IN (`+in+`)`, chunk...); err != nil {
				return 0, 0, fmt.Errorf("catalog: tier migration: delete %s: %w", d.table, err)
			}
		}
	}
	last := batch[len(batch)-1].head
	if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_meta(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, tierMetaCursor, strconv.FormatInt(int64(last), 10)); err != nil {
		return 0, 0, err
	}
	next := *p
	next.Removed += removed
	next.Rescued += rescued
	if err := c.saveTierProgress(ctx, tx, next); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	*p = next
	return removed, rescued, nil
}

// tierBatchRanges are the key ranges a batch's touches can fall in: its
// movies, its shows, and those shows' seasons and episodes.
func tierBatchRanges(batch []tierGroup) []keyRange {
	var movies, shows []core.RatingKey
	for _, g := range batch {
		switch g.head.Kind() {
		case core.KindMovie:
			movies = append(movies, g.head)
		case core.KindShow:
			shows = append(shows, g.head)
		}
	}
	var out []keyRange
	if len(movies) > 0 {
		out = append(out, keyRange{slices.Min(movies), slices.Max(movies)})
	}
	if len(shows) > 0 {
		lo, hi := slices.Min(shows).TMDB(), slices.Max(shows).TMDB()
		out = append(out,
			keyRange{core.ShowKey(lo), core.ShowKey(hi)},
			keyRange{core.SeasonKey(lo, 0), core.SeasonKey(hi, 9999)},
			keyRange{core.EpisodeKey(lo, 0, 0), core.EpisodeKey(hi, 999, 9999)})
	}
	return out
}

// sweepTierTouched re-reads every touch source and stores again, through
// Get, each touched movie or show that has no row. It returns how many it
// stored.
func (c *Catalog) sweepTierTouched(ctx context.Context) (int, error) {
	touched, _, err := c.collectTouched(ctx)
	if err != nil {
		return 0, err
	}
	keys := make([]core.RatingKey, 0, len(touched.keys))
	for k := range touched.keys {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	missing, err := c.missingKeys(ctx, keys)
	if err != nil {
		return 0, err
	}
	promoted := 0
	if len(missing) > 0 {
		c.log.Warn("catalog: tier migration: touched titles without a row; storing them again", "titles", len(missing))
		for i, k := range missing {
			if i >= tierSweepMax || c.tmdb() == nil {
				break
			}
			if t, err := c.Get(ctx, k); err == nil && t != nil {
				promoted++
			}
			if !waitGap(ctx, time.Second/tierSweepRate) {
				return promoted, ctx.Err()
			}
		}
	}
	if _, err := c.promoteCollectionTouched(ctx); err != nil {
		return promoted, err
	}
	return promoted, nil
}

// missingKeys returns the keys in sorted keys with no catalog_titles row.
func (c *Catalog) missingKeys(ctx context.Context, keys []core.RatingKey) ([]core.RatingKey, error) {
	var missing []core.RatingKey
	for start := 0; start < len(keys); start += 500 {
		chunk := keys[start:min(start+500, len(keys))]
		args := make([]any, len(chunk))
		for i, k := range chunk {
			args[i] = int64(k)
		}
		rows, err := c.db.QueryContext(ctx, `SELECT key FROM catalog_titles WHERE key IN (`+placeholders(len(args))+`)`, args...)
		if err != nil {
			return nil, err
		}
		have := map[core.RatingKey]bool{}
		for rows.Next() {
			var k int64
			if err := rows.Scan(&k); err != nil {
				rows.Close()
				return nil, err
			}
			have[core.RatingKey(k)] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for _, k := range chunk {
			if !have[k] {
				missing = append(missing, k)
			}
		}
	}
	return missing, nil
}

// MigrateTiers runs the tier migration once, to completion or the first
// error (tests and tools; the server runs it from Run, with retries).
func (c *Catalog) MigrateTiers(ctx context.Context) error { return c.migrateTiers(ctx) }

// UseTailExports builds the long-tail index from TMDB export readers
// (gzipped JSON lines) and installs it, for tests and tools that check a
// migrated catalogue's search.
func (c *Catalog) UseTailExports(movies, tv io.Reader) error {
	t, err := buildTailIndex(movies, tv, nil, nil)
	if err != nil {
		return err
	}
	c.setTailIndex(t)
	return nil
}
