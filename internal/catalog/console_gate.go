package catalog

import (
	"strconv"

	"github.com/braedonsaunders/ether/internal/core"
)

// Expected policies are computed from settings/feed generations, never from
// title scans. Published generations remain immutable while being read.
func (c *Catalog) SetConsoleGatePolicies(provider func() (string, string)) {
	c.consoleGatePolicies.Store(&provider)
}

// SetConsoleGateFilter installs the sticky availability_gate filter provider for
// owner console ReleaseGate queries ("held", "admitted", "unknown").
func (c *Catalog) SetConsoleGateFilter(provider func() core.Filter) {
	c.consoleGateFilter.Store(&provider)
}

// ConsoleGateChanged records a new published gate change. Ordinary browse
// filters carry the generations they read, so only release-gate results
// are keyed by it (consoleGateKey); the catalogue's other caches stay.
func (c *Catalog) ConsoleGateChanged() { c.consoleGateGen.Add(1) }

// consoleGateKey separates cached release-gate results by gate publication;
// it is empty for filters without a release gate.
func (c *Catalog) consoleGateKey(f core.Filter) string {
	if !hasConsoleGate(f) {
		return ""
	}
	return "|gate-gen:" + strconv.FormatUint(c.consoleGateGen.Load(), 10)
}

func (c *Catalog) consoleGateConditions(f core.Filter) ([]string, []any) {
	if f.ReleaseGate == "" {
		return nil, nil
	}
	if gfFn := c.consoleGateFilter.Load(); gfFn != nil {
		gf := (*gfFn)()
		nowTS := c.now().Unix()
		mc, ma := movieGateSQL("t", gf, nowTS)
		sc, sa := showGateSQL("t", gf, nowTS)
		movieKnown := mc != ""
		showKnown := sc != ""
		var knownConds []string
		if movieKnown {
			knownConds = append(knownConds, "t.type='movie'")
		}
		if showKnown {
			knownConds = append(knownConds, "t.type='show'")
		}
		known := "1=0"
		if len(knownConds) == 1 {
			known = knownConds[0]
		} else if len(knownConds) == 2 {
			known = "(t.type='movie' OR t.type='show')"
		}
		switch f.ReleaseGate {
		case "unknown":
			return []string{"NOT (" + known + ")"}, nil
		case "admitted":
			if !movieKnown && !showKnown {
				return []string{"1=0"}, nil
			}
			var parts []string
			var args []any
			if movieKnown {
				parts = append(parts, "(t.type='movie' AND "+mc+")")
				args = append(args, ma...)
			}
			if showKnown {
				parts = append(parts, "(t.type='show' AND "+sc+")")
				args = append(args, sa...)
			}
			if len(parts) == 1 {
				return []string{parts[0]}, args
			}
			return []string{"(" + parts[0] + " OR " + parts[1] + ")"}, args
		case "held":
			if !movieKnown && !showKnown {
				return []string{"1=0"}, nil
			}
			var parts []string
			var args []any
			if movieKnown {
				parts = append(parts, "(t.type='movie' AND t.released_ts>? AND NOT "+mc+")")
				args = append(args, gf.MovieGateCutoff)
				args = append(args, ma...)
			}
			if showKnown {
				parts = append(parts, "(t.type='show' AND t.released_ts>? AND NOT "+sc+")")
				args = append(args, gf.ShowGateCutoff)
				args = append(args, sa...)
			}
			if len(parts) == 1 {
				return []string{parts[0]}, args
			}
			return []string{"(" + parts[0] + " OR " + parts[1] + ")"}, args
		default:
			return []string{"1=0"}, nil
		}
	}
	p := c.consoleGatePolicies.Load()
	if p == nil {
		if f.ReleaseGate == "unknown" {
			return []string{"1=1"}, nil
		}
		return []string{"1=0"}, nil
	}
	movie, show := (*p)()
	known := `EXISTS(SELECT 1 FROM availability_hidden_current gc WHERE gc.kind=t.type AND ((gc.kind='movie' AND gc.policy=?) OR (gc.kind='show' AND gc.policy=?)))`
	held := `t.key IN (SELECT gh.rating_key FROM availability_hidden_current gc JOIN availability_hidden gh ON gh.kind=gc.kind AND gh.from_seq<=gc.seq AND gh.to_seq>gc.seq WHERE ((gc.kind='movie' AND gc.policy=?) OR (gc.kind='show' AND gc.policy=?)))`
	switch f.ReleaseGate {
	case "held":
		return []string{held}, []any{movie, show}
	case "admitted":
		return []string{known, "NOT (" + held + ")"}, []any{movie, show, movie, show}
	case "unknown":
		return []string{"NOT " + known}, []any{movie, show}
	}
	return []string{"1=0"}, nil
}

func hasConsoleGate(f core.Filter) bool {
	if f.ReleaseGate != "" {
		return true
	}
	var group func(*core.FilterGroup) bool
	group = func(g *core.FilterGroup) bool {
		if g == nil {
			return false
		}
		if g.Filter != nil && hasConsoleGate(*g.Filter) {
			return true
		}
		for i := range g.Children {
			if group(&g.Children[i]) {
				return true
			}
		}
		return false
	}
	return group(f.Group)
}

func (c *Catalog) consoleAvailabilityCacheKey(f core.Filter) string {
	key := c.evidencePolicy()
	if hasConsoleGate(f) {
		if gfFn := c.consoleGateFilter.Load(); gfFn != nil {
			gf := (*gfFn)()
			key += "|gate:" + gf.GatePolicy + ":" + strconv.FormatInt(minuteBound(gf.MovieGateCutoff), 10) + ":" + strconv.FormatInt(minuteBound(gf.ShowGateCutoff), 10)
		} else if p := c.consoleGatePolicies.Load(); p != nil {
			movie, show := (*p)()
			key += "|gate:" + movie + ":" + show
		}
	}
	return key + c.consoleGateKey(f)
}
