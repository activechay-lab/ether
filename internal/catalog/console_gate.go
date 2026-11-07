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

// ConsoleGateChanged records a new published hidden set. Ordinary browse
// filters carry the generations they read, so only release-gate results
// are keyed by it (consoleGateKey); the catalogue's other caches stay.
func (c *Catalog) ConsoleGateChanged() { c.consoleGateGen.Add(1) }

// consoleGateKey separates cached release-gate results by hidden-set
// publication; it is empty for filters without a release gate.
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
	p := c.consoleGatePolicies.Load()
	if p == nil {
		if f.ReleaseGate == "unknown" {
			return []string{"1=1"}, nil
		}
		return []string{"1=0"}, nil
	}
	movie, show := (*p)()
	known := `EXISTS(SELECT 1 FROM availability_hidden_current gc WHERE gc.kind=t.type AND ((gc.kind='movie' AND gc.policy=?) OR (gc.kind='show' AND gc.policy=?)))`
	held := `t.key IN (SELECT gh.rating_key FROM availability_hidden_current gc JOIN availability_hidden gh ON gh.kind=gc.kind AND gh.generation=gc.generation WHERE ((gc.kind='movie' AND gc.policy=?) OR (gc.kind='show' AND gc.policy=?)))`
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
		if p := c.consoleGatePolicies.Load(); p != nil {
			movie, show := (*p)()
			key += "|gate:" + movie + ":" + show
		}
	}
	return key + c.consoleGateKey(f)
}
