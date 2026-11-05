package newznab

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	// The daily API and grab limits an indexer reports about the account
	// (<newznab:apilimits apimax="…" grabmax="…"/>), remembered across
	// restarts so background work sizes itself before the first response.
	store.RegisterAfter("newznab_limits", "newznab")
	store.Register("newznab_limits", 1, `CREATE TABLE newznab_api_limits (indexer_id TEXT PRIMARY KEY, api_max INTEGER NOT NULL, grab_max INTEGER NOT NULL DEFAULT 0);`)
}

var (
	apiMaxAttr  = regexp.MustCompile(`(?i)apimax\W{0,4}(\d{1,9})`)
	grabMaxAttr = regexp.MustCompile(`(?i)grabmax\W{0,4}(\d{1,9})`)
)

// reportedLimits reads the account's daily API request and NZB grab
// limits from a response that carries newznab:apilimits (nZEDb,
// newznab-tmux and the indexers built on them, in XML or JSON). Zero means
// not reported.
func reportedLimits(body []byte) (api, grabs int) {
	head := body[:min(len(body), 8192)]
	i := bytes.Index(bytes.ToLower(head), []byte("apilimits"))
	if i < 0 {
		return 0, 0
	}
	tag := head[i:min(len(head), i+400)]
	if m := apiMaxAttr.FindSubmatch(tag); m != nil {
		api, _ = strconv.Atoi(string(m[1]))
	}
	if m := grabMaxAttr.FindSubmatch(tag); m != nil {
		grabs, _ = strconv.Atoi(string(m[1]))
	}
	return api, grabs
}

type apiLimitState struct {
	mu        sync.Mutex
	loaded    bool
	api, grab int
}

// noteAPILimit remembers limits the indexer reported in body.
func (c *Client) noteAPILimit(ctx context.Context, body []byte) {
	api, grab := reportedLimits(body)
	if api <= 0 && grab <= 0 {
		return
	}
	c.loadLimits()
	c.apiLimit.mu.Lock()
	if api <= 0 {
		api = c.apiLimit.api
	}
	if grab <= 0 {
		grab = c.apiLimit.grab
	}
	changed := c.apiLimit.api != api || c.apiLimit.grab != grab
	c.apiLimit.api, c.apiLimit.grab = api, grab
	c.apiLimit.mu.Unlock()
	if !changed || c.db == nil {
		return
	}
	if _, err := c.db.ExecContext(ctx, `INSERT INTO newznab_api_limits(indexer_id,api_max,grab_max) VALUES(?,?,?) ON CONFLICT(indexer_id) DO UPDATE SET api_max=excluded.api_max,grab_max=excluded.grab_max`, c.indexerID(), api, grab); err != nil {
		c.log.Debug("could not remember indexer API limit", "indexer", c.Name(), "err", err)
	}
}

func (c *Client) loadLimits() {
	c.apiLimit.mu.Lock()
	defer c.apiLimit.mu.Unlock()
	if !c.apiLimit.loaded && c.db != nil {
		var api, grab int
		if err := c.db.QueryRowContext(context.Background(), `SELECT api_max,grab_max FROM newznab_api_limits WHERE indexer_id=?`, c.indexerID()).Scan(&api, &grab); err == nil {
			c.apiLimit.api, c.apiLimit.grab = api, grab
		}
	}
	c.apiLimit.loaded = true
}

// reportedLimit is the daily API (kind "hit") or grab limit the indexer
// last reported, or 0.
func (c *Client) reportedLimit(kind string) int {
	c.loadLimits()
	c.apiLimit.mu.Lock()
	defer c.apiLimit.mu.Unlock()
	if kind == "grab" {
		return c.apiLimit.grab
	}
	return c.apiLimit.api
}

// DailyAPILimit is the indexer's daily API request limit: the owner's
// configured cap, otherwise the one the indexer reports, otherwise 0
// (unknown). Background work takes only its share of it.
func (c *Client) DailyAPILimit() int {
	if n := c.Config().DailyLimit; n > 0 {
		return n
	}
	return c.reportedLimit("hit")
}

// dailyLimitOrAssumed is the indexer's daily limit for kind ("hit" or
// "grab"): configured, else reported, else the strictest tier's.
func (c *Client) dailyLimitOrAssumed(kind string) int {
	n := c.Config().DailyLimit
	if kind == "grab" {
		n = c.Config().DailyGrabs
	}
	if n <= 0 {
		n = c.reportedLimit(kind)
	}
	if n <= 0 {
		n = config.AssumedDailyLimit
	}
	return n
}

// speculativeShare is the percentage of an indexer's daily limit that
// work nobody is waiting on may spend: unattended warm-ups (preloads,
// prefetches, watchlist and cache-fill checks, detail warm-ups not yet
// promoted to Play), detail-page version discovery and availability grabs.
// Playback keeps the rest.
const speculativeShare = 10

// Usage labels whose requests draw on the speculative share. Availability
// searches have their own allowance (internal/availability); their grabs
// share this one.
const (
	speculativeHitPurposes  = "'warmup','detail'"
	speculativeGrabPurposes = "'warmup','detail','availability'"
)

// usagePurpose is the purpose a request is recorded and budgeted under: ctx's
// tag, except that a warm-up a viewer promoted to Play is playback from then
// on. Unpromoted warm-ups stay warm-ups, so the speculative share counts only
// speculative requests.
func usagePurpose(ctx context.Context) string {
	p := core.IndexerPurpose(ctx)
	if p == core.IndexerPurposeWarmup && !core.IsUnattendedSearch(ctx) {
		return core.IndexerPurposePlayback
	}
	return p
}

// speculativeRoom refuses a speculative request once today's speculative
// requests of kind reached their share. Purpose is inherited: a fallback
// query, caps-free retry or rescue started by speculative work keeps its
// label (Manager.SearchRescueWith), so it is counted and refused here too.
// The count is today's newznab_usage rows.
func (c *Client) speculativeRoom(ctx context.Context, kind string) error {
	purposes := speculativeHitPurposes
	col := "hits"
	if kind == "grab" {
		purposes, col = speculativeGrabPurposes, "grabs"
	}
	if !strings.Contains(purposes, "'"+usagePurpose(ctx)+"'") {
		return nil
	}
	db := c.db
	if db == nil {
		db = c.usageDB
	}
	if db == nil {
		return nil
	}
	var used int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(`+col+`),0) FROM newznab_usage WHERE indexer_id=? AND day=? AND purpose IN (`+purposes+`)`, c.indexerID(), c.today()).Scan(&used); err != nil {
		return err
	}
	if used >= max(1, c.dailyLimitOrAssumed(kind)*speculativeShare/100) {
		return fmt.Errorf("indexer %q background %s share: %w", c.Name(), kind, core.ErrBudgetExhausted)
	}
	return nil
}
