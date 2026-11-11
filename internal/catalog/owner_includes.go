package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/braedonsaunders/ether/internal/clusterbus"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/libraryfilter"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("catalog", 58, `CREATE TABLE catalog_owner_includes (key INTEGER PRIMARY KEY, added_at INTEGER NOT NULL DEFAULT 0);`)
}

type ownerIncludeCache struct {
	sync.RWMutex
	keys map[core.RatingKey]struct{}
	bus  *clusterbus.Invalidator
}

// BrowsabilityReason describes one rule that keeps a title out of the owner's
// browsable library, computed by the exact same predicates Browse applies.
type BrowsabilityReason struct {
	Code          string  `json:"code"`
	Message       string  `json:"message"`
	Votes         int     `json:"votes,omitempty"`
	MinVotes      int     `json:"minVotes,omitempty"`
	Popularity    float64 `json:"popularity,omitempty"`
	MinPopularity float64 `json:"minPopularity,omitempty"`
	Rule          string  `json:"rule,omitempty"`
}

// BrowsabilityStatus reports whether a title is browsable in the owner's
// library, why it is not, and whether an owner include may promote it.
type BrowsabilityStatus struct {
	Browsable     bool                 `json:"browsable"`
	Included      bool                 `json:"included"`
	InLibraryTier bool                 `json:"inLibraryTier"`
	CanInclude    bool                 `json:"canInclude"`
	ReleaseGated  bool                 `json:"releaseGated"`
	Reasons       []string             `json:"reasons"`
	Items         []BrowsabilityReason `json:"items"`
}

// IncludeRejectedError reports why a title cannot be force-included in the
// library (adult, unreleased, release-gated, or blocked by content policy).
type IncludeRejectedError struct {
	Message string
}

func (e *IncludeRejectedError) Error() string  { return e.Message }
func (e *IncludeRejectedError) Conflict() bool { return true }

// SetOwnerContentPolicy installs the mandatory owner content policy check
// (internal/safety). An owner include never bypasses this check.
func (c *Catalog) SetOwnerContentPolicy(fn func(context.Context, *core.Title) bool) {
	if c == nil {
		return
	}
	c.ownerPolicy.Store(&fn)
}

// SetReleaseGate installs the new-release playability gate
// (internal/availability). An owner include never ungates an unreleased or
// unplayable new release.
func (c *Catalog) SetReleaseGate(fn func(context.Context, core.Title) bool) {
	if c == nil {
		return
	}
	c.releaseGate.Store(&fn)
}

// SetIncludesBus connects the cluster invalidator for catalog_owner_includes.
func (c *Catalog) SetIncludesBus(bus clusterbus.Bus) {
	if c == nil {
		return
	}
	iv := clusterbus.NewInvalidator(bus, "catalog_owner_includes", c.log, func(ctx context.Context, _ []string, _ bool) {
		c.configUpdateMu.Lock()
		defer c.configUpdateMu.Unlock()
		if err := c.reloadOwnerIncludes(ctx); err != nil {
			c.log.Error("catalog: reload owner includes", "err", err)
			return
		}
		c.writeGen.Add(1)
		c.listMu.Lock()
		c.listCache = map[string]listEntry{}
		c.listMu.Unlock()
	})
	c.ownerIncludes.Lock()
	old := c.ownerIncludes.bus
	c.ownerIncludes.bus = iv
	c.ownerIncludes.Unlock()
	old.Close()
}

func (c *Catalog) reloadOwnerIncludes(ctx context.Context) error {
	rows, err := c.db.QueryContext(ctx, `SELECT key FROM catalog_owner_includes`)
	if err != nil {
		return err
	}
	defer rows.Close()
	keys := map[core.RatingKey]struct{}{}
	for rows.Next() {
		var k int64
		if err := rows.Scan(&k); err != nil {
			return err
		}
		keys[core.RatingKey(k)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	c.ownerIncludes.Lock()
	c.ownerIncludes.keys = keys
	c.ownerIncludes.Unlock()
	return nil
}

func (c *Catalog) isOwnerIncluded(key core.RatingKey) bool {
	if c == nil || key == 0 {
		return false
	}
	c.ownerIncludes.RLock()
	_, ok := c.ownerIncludes.keys[key]
	c.ownerIncludes.RUnlock()
	return ok
}

func (c *Catalog) isTitleOrShowOwnerIncluded(t *core.Title) bool {
	if c == nil || t == nil {
		return false
	}
	if c.isOwnerIncluded(t.Key) {
		return true
	}
	return t.ShowKey != 0 && c.isOwnerIncluded(t.ShowKey)
}

func (c *Catalog) hasOwnerIncludes() bool {
	if c == nil {
		return false
	}
	c.ownerIncludes.RLock()
	n := len(c.ownerIncludes.keys)
	c.ownerIncludes.RUnlock()
	return n > 0
}

// OwnerIncluded performs one indexed primary-key lookup against catalog_owner_includes.
func (c *Catalog) OwnerIncluded(ctx context.Context, key core.RatingKey) (bool, error) {
	var one int
	err := c.db.QueryRowContext(ctx, `SELECT 1 FROM catalog_owner_includes WHERE key=?`, int64(key)).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// policySQLHidden returns the SQL predicate that keeps non-hidden rows for
// viewerID, preserving zero extra SQL when the policy is passive or no owner
// includes exist, and keeping custom viewer rules enforced.
func (c *Catalog) policySQLHidden(viewerID int64, policy *libraryfilter.Filter) (string, []any) {
	q, a := policy.SQLHidden()
	if !policy.Passive() && !c.hasCustomViewerRules(viewerID) && c.hasOwnerIncludes() {
		return "((NOT (" + q + ")) OR EXISTS (SELECT 1 FROM catalog_owner_includes oi WHERE oi.key=t.key LIMIT 1 OFFSET 0))", a
	}
	return "NOT (" + q + ")", a
}

func meetsBars(cfg config.Catalog, t core.Title) bool {
	minPop, minVotes := cfg.Bars()
	popOK := minPop > 0 && t.Popularity >= minPop
	voteOK := minVotes > 0 && t.VoteCount >= minVotes
	return popOK || voteOK
}

func (c *Catalog) configAndVisibility() (config.Catalog, *libraryfilter.Filter) {
	c.cfgMu.RLock()
	defer c.cfgMu.RUnlock()
	return c.cfg, c.visibility
}

// ExplainBrowsability evaluates why key is or is not in the owner's browsable
// library using the same gates Browse, availability discovery, and owner
// content policy apply.
func (c *Catalog) ExplainBrowsability(ctx context.Context, key core.RatingKey) (BrowsabilityStatus, error) {
	if key.Kind() != core.KindMovie && key.Kind() != core.KindShow {
		return BrowsabilityStatus{}, core.ErrNotFound
	}
	st, err := c.loadByKey(ctx, key)
	var t core.Title
	inLibraryTier := false
	switch {
	case err == nil:
		t = st.t
		inLibraryTier = st.browsable
	case errors.Is(err, core.ErrNotFound):
		if client := c.tmdb(); client != nil {
			fetched, ferr := c.fetchUnstoredTitle(ctx, client, key)
			if ferr == nil {
				t = fetched
			} else if summary, ok := c.summaryTitle(key); ok {
				t = summary
			} else {
				return BrowsabilityStatus{}, ferr
			}
		} else if summary, ok := c.summaryTitle(key); ok {
			t = summary
		} else {
			return BrowsabilityStatus{}, core.ErrNotFound
		}
	default:
		return BrowsabilityStatus{}, err
	}

	included, err := c.OwnerIncluded(ctx, key)
	if err != nil {
		return BrowsabilityStatus{}, err
	}
	return c.evaluateBrowsability(ctx, st, t, included, inLibraryTier), nil
}

func (c *Catalog) fetchUnstoredTitle(ctx context.Context, client TMDB, key core.RatingKey) (core.Title, error) {
	switch key.Kind() {
	case core.KindMovie:
		d, err := client.MovieDetails(ctx, key.TMDB())
		if err != nil {
			return core.Title{}, err
		}
		t := c.movieDetailsToTitle(ctx, d)
		if d.ReleaseDates != nil {
			t.HomeReleaseChecked = true
			t.HomeReleased = earliestDatedRelease(d.ReleaseDates, 4, 5)
		}
		return t, nil
	case core.KindShow:
		d, err := client.TVDetails(ctx, key.TMDB())
		if err != nil {
			return core.Title{}, err
		}
		return c.tvDetailsToTitle(ctx, d), nil
	default:
		return core.Title{}, core.ErrNotFound
	}
}

func (c *Catalog) evaluateBrowsability(ctx context.Context, st *storedTitle, t core.Title, included, inLibraryTier bool) BrowsabilityStatus {
	cfg, vis := c.configAndVisibility()
	now := c.now()
	items := []BrowsabilityReason{}
	add := func(r BrowsabilityReason) {
		items = append(items, r)
	}
	hardBlocked := false
	releaseGated := false

	if t.Adult {
		hardBlocked = true
		add(BrowsabilityReason{Code: "adult", Message: "Adult titles are off"})
	}

	released := t.IsReleased(now)
	upcomingAllowed := cfg.ShowUpcomingMovies && t.Type == core.TypeMovie && !t.Released.IsZero() && t.Released.After(now)
	if !released {
		// Owner rule: never ungate unreleased titles via Add to library.
		hardBlocked = true
		releaseGated = true
		if !upcomingAllowed {
			add(BrowsabilityReason{Code: "unreleased", Message: "Not released yet"})
		}
	}

	if released {
		digitalGated := cfg.ReleaseMode == config.ModeDigital && !homeReleaseVisible(&t, now, cfg.HomeReleaseOffsetDays)
		availGated := false
		if gate := c.releaseGate.Load(); gate != nil && *gate != nil {
			availGated = !(*gate)(ctx, t)
		}
		if digitalGated || availGated {
			hardBlocked = true
			releaseGated = true
			if t.Type == core.TypeMovie {
				add(BrowsabilityReason{Code: "release_gate_movie", Message: "Waiting for a playable release (new movie)"})
			} else {
				add(BrowsabilityReason{Code: "release_gate_show", Message: "Waiting for a playable release"})
			}
		}
	}

	if !t.Adult {
		if policy := c.ownerPolicy.Load(); policy != nil && *policy != nil && !(*policy)(ctx, &t) {
			hardBlocked = true
			add(BrowsabilityReason{Code: "content_policy", Message: "Excluded by your content rules"})
		}
	}

	if !included {
		storedBrowsable := st != nil && st.browsable
		if !storedBrowsable || !released {
			if !meetsBars(cfg, t) {
				minPop, minVotes := cfg.Bars()
				if minVotes > 0 {
					add(BrowsabilityReason{
						Code:          "popularity_floor",
						Votes:         t.VoteCount,
						MinVotes:      minVotes,
						Popularity:    t.Popularity,
						MinPopularity: minPop,
						Message:       fmt.Sprintf("Below your popularity floor (%d votes; floor %d)", t.VoteCount, minVotes),
					})
				} else {
					add(BrowsabilityReason{
						Code:          "popularity_only_floor",
						Popularity:    t.Popularity,
						MinPopularity: minPop,
						Message:       fmt.Sprintf("Below your popularity floor (%.1f popularity; floor %.1f)", t.Popularity, minPop),
					})
				}
			} else if !storedBrowsable {
				add(BrowsabilityReason{Code: "search_only", Message: "In the search-only index"})
			}
		}
		if hidden, idx, byFallback := vis.MatchedHideRule(&t); hidden {
			switch {
			case byFallback:
				add(BrowsabilityReason{Code: "catalog_default_hide", Message: "Hidden by your default catalogue visibility"})
			case strings.TrimSpace(vis.RuleName(idx)) != "":
				name := strings.TrimSpace(vis.RuleName(idx))
				add(BrowsabilityReason{
					Code:    "catalog_rule_named",
					Rule:    name,
					Message: fmt.Sprintf("Excluded by your catalogue rule “%s”", name),
				})
			default:
				add(BrowsabilityReason{Code: "catalog_rule", Message: "Excluded by your catalogue rules"})
			}
		}
	} else if st == nil || !st.browsable {
		add(BrowsabilityReason{Code: "search_only", Message: "In the search-only index"})
	}

	reasons := make([]string, len(items))
	for i, item := range items {
		reasons[i] = item.Message
	}
	browsable := len(items) == 0 && st != nil && (st.browsable || upcomingAllowed)
	return BrowsabilityStatus{
		Browsable:     browsable,
		Included:      included,
		InLibraryTier: inLibraryTier,
		CanInclude:    !browsable && !included && !hardBlocked,
		ReleaseGated:  releaseGated,
		Reasons:       reasons,
		Items:         items,
	}
}

func (c *Catalog) hardBlockReason(ctx context.Context, t core.Title) string {
	now := c.now()
	cfg := c.config()
	if t.Adult {
		return "Adult titles are off"
	}
	if !t.IsReleased(now) {
		return "This title is not released yet and will appear once a playable release exists"
	}
	digitalGated := cfg.ReleaseMode == config.ModeDigital && !homeReleaseVisible(&t, now, cfg.HomeReleaseOffsetDays)
	availGated := false
	if gate := c.releaseGate.Load(); gate != nil && *gate != nil {
		availGated = !(*gate)(ctx, t)
	}
	if digitalGated || availGated {
		return "This title is waiting for a playable release and cannot be added early"
	}
	if policy := c.ownerPolicy.Load(); policy != nil && *policy != nil && !(*policy)(ctx, &t) {
		return "Excluded by your content rules"
	}
	return ""
}

// IncludeTitle records an owner include for key, promotes it to the library
// tier (fetching full details when TMDB is available), and marks it browsable.
// It refuses adult, unreleased, release-gated, and content-policy-excluded titles.
func (c *Catalog) IncludeTitle(ctx context.Context, key core.RatingKey) (BrowsabilityStatus, error) {
	if key.Kind() != core.KindMovie && key.Kind() != core.KindShow {
		return BrowsabilityStatus{}, core.ErrNotFound
	}
	st, err := c.loadByKey(ctx, key)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return BrowsabilityStatus{}, err
	}

	needDetail := errors.Is(err, core.ErrNotFound) || !st.detail
	if needDetail && c.tmdb() != nil {
		client := c.tmdb()
		switch key.Kind() {
		case core.KindMovie:
			d, ferr := client.MovieDetails(ctx, key.TMDB())
			if ferr != nil {
				if st == nil {
					return BrowsabilityStatus{}, ferr
				}
			} else {
				t := c.movieDetailsToTitle(ctx, d)
				if d.ReleaseDates != nil {
					t.HomeReleaseChecked = true
					t.HomeReleased = earliestDatedRelease(d.ReleaseDates, 4, 5)
				}
				if reason := c.hardBlockReason(ctx, t); reason != "" {
					return BrowsabilityStatus{}, &IncludeRejectedError{Message: reason}
				}
				var crowd *detailExtra
				if d.Recommendations != nil {
					crowd = c.crowdExtra(ctx, key, movieCrowdTitles(c, ctx, d.Recommendations))
				}
				if werr := c.writeDetail(ctx, t, upsertOpts{browsable: true, detail: true, releaseDates: d.ReleaseDates}, crowd); werr != nil {
					return BrowsabilityStatus{}, werr
				}
				st, err = c.loadByKey(ctx, key)
				if err != nil {
					return BrowsabilityStatus{}, err
				}
			}
		case core.KindShow:
			d, ferr := client.TVDetails(ctx, key.TMDB())
			if ferr != nil {
				if st == nil {
					return BrowsabilityStatus{}, ferr
				}
			} else {
				t := c.tvDetailsToTitle(ctx, d)
				if reason := c.hardBlockReason(ctx, t); reason != "" {
					return BrowsabilityStatus{}, &IncludeRejectedError{Message: reason}
				}
				var crowd *detailExtra
				if d.Recommendations != nil {
					crowd = c.crowdExtra(ctx, key, tvCrowdTitles(c, ctx, d.Recommendations))
				}
				if werr := c.storeShowDetails(ctx, t, d, crowd); werr != nil {
					return BrowsabilityStatus{}, werr
				}
				st, err = c.loadByKey(ctx, key)
				if err != nil {
					return BrowsabilityStatus{}, err
				}
			}
		}
	}

	if st == nil {
		summary, ok := c.summaryTitle(key)
		if !ok {
			return BrowsabilityStatus{}, core.ErrNotFound
		}
		if reason := c.hardBlockReason(ctx, summary); reason != "" {
			return BrowsabilityStatus{}, &IncludeRejectedError{Message: reason}
		}
		if uerr := c.upsert(ctx, summary, upsertOpts{browsable: true}); uerr != nil {
			return BrowsabilityStatus{}, uerr
		}
		st, err = c.loadByKey(ctx, key)
		if err != nil {
			return BrowsabilityStatus{}, err
		}
	} else {
		if reason := c.hardBlockReason(ctx, st.t); reason != "" {
			return BrowsabilityStatus{}, &IncludeRejectedError{Message: reason}
		}
	}

	c.configUpdateMu.Lock()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		c.configUpdateMu.Unlock()
		return BrowsabilityStatus{}, err
	}
	now := c.now().Unix()
	if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_owner_includes(key,added_at) VALUES(?,?) ON CONFLICT(key) DO NOTHING`, int64(key), now); err != nil {
		_ = tx.Rollback()
		c.configUpdateMu.Unlock()
		return BrowsabilityStatus{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE catalog_titles SET browsable=1 WHERE key=? AND browsable=0`, int64(key)); err != nil {
		_ = tx.Rollback()
		c.configUpdateMu.Unlock()
		return BrowsabilityStatus{}, err
	}
	if err = tx.Commit(); err != nil {
		c.configUpdateMu.Unlock()
		return BrowsabilityStatus{}, err
	}
	if err := c.reloadOwnerIncludes(ctx); err != nil {
		c.configUpdateMu.Unlock()
		return BrowsabilityStatus{}, err
	}
	c.cacheMu.Lock()
	delete(c.titleCache, key)
	c.cacheMu.Unlock()
	c.writeGen.Add(1)
	c.listMu.Lock()
	c.listCache = map[string]listEntry{}
	c.listMu.Unlock()
	c.ownerIncludes.RLock()
	iv := c.ownerIncludes.bus
	c.ownerIncludes.RUnlock()
	c.configUpdateMu.Unlock()
	iv.Notify(strconv.FormatInt(int64(key), 10))

	return c.ExplainBrowsability(ctx, key)
}

// RemoveInclude removes an owner include and demotes the title back out of the
// browsable set when it does not meet the configured popularity/vote bars on
// its own.
func (c *Catalog) RemoveInclude(ctx context.Context, key core.RatingKey) (BrowsabilityStatus, error) {
	if key.Kind() != core.KindMovie && key.Kind() != core.KindShow {
		return BrowsabilityStatus{}, core.ErrNotFound
	}
	st, err := c.loadByKey(ctx, key)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return BrowsabilityStatus{}, err
	}

	c.configUpdateMu.Lock()
	keepBrowsable := st != nil && MeetsThreshold(c.config(), st.t, c.now())
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		c.configUpdateMu.Unlock()
		return BrowsabilityStatus{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_owner_includes WHERE key=?`, int64(key)); err != nil {
		_ = tx.Rollback()
		c.configUpdateMu.Unlock()
		return BrowsabilityStatus{}, err
	}
	if st != nil && st.browsable && !keepBrowsable {
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_titles SET browsable=0 WHERE key=? AND browsable=1`, int64(key)); err != nil {
			_ = tx.Rollback()
			c.configUpdateMu.Unlock()
			return BrowsabilityStatus{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		c.configUpdateMu.Unlock()
		return BrowsabilityStatus{}, err
	}
	if err := c.reloadOwnerIncludes(ctx); err != nil {
		c.configUpdateMu.Unlock()
		return BrowsabilityStatus{}, err
	}
	c.cacheMu.Lock()
	delete(c.titleCache, key)
	c.cacheMu.Unlock()
	c.writeGen.Add(1)
	c.listMu.Lock()
	c.listCache = map[string]listEntry{}
	c.listMu.Unlock()
	c.ownerIncludes.RLock()
	iv := c.ownerIncludes.bus
	c.ownerIncludes.RUnlock()
	c.configUpdateMu.Unlock()
	iv.Notify(strconv.FormatInt(int64(key), 10))

	if st == nil {
		if _, ok := c.summaryTitle(key); !ok && c.tmdb() == nil {
			return BrowsabilityStatus{}, core.ErrNotFound
		}
	}
	return c.ExplainBrowsability(ctx, key)
}
