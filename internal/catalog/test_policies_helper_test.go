package catalog

import (
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func testPolicies(rules []core.LibraryExclusion, def string) LibraryPolicies {
	var pol LibraryPolicies
	pol.MovieDefault = def
	pol.ShowDefault = def
	for _, r := range rules {
		switch strings.ToLower(strings.TrimSpace(r.MediaType)) {
		case "movie":
			c := r
			pol.MovieRules = append(pol.MovieRules, c)
		case "show":
			c := r
			pol.ShowRules = append(pol.ShowRules, c)
		default:
			mc := r
			mc.MediaType = "movie"
			pol.MovieRules = append(pol.MovieRules, mc)
			sc := r
			sc.MediaType = "show"
			pol.ShowRules = append(pol.ShowRules, sc)
		}
	}
	return pol
}

func setTestPolicies(t testing.TB, c *Catalog, rules []core.LibraryExclusion, def string) {
	t.Helper()
	if err := c.SetLibraryPolicies(testPolicies(rules, def)); err != nil {
		t.Fatal(err)
	}
}
