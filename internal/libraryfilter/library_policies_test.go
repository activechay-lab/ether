package libraryfilter

import (
	"encoding/json"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestCompileLibraryPolicies(t *testing.T) {
	movieRules := []core.LibraryExclusion{{
		Name: "Hide French Movies", MediaType: "movie", Match: "all",
		Rules: []core.CollectionRule{{Field: "language", Op: "is", Value: json.RawMessage(`"fr"`)}},
	}}
	showRules := []core.LibraryExclusion{{
		Name: "Allow Drama Shows", Action: "show", MediaType: "show", Match: "all",
		Rules: []core.CollectionRule{{Field: "genre", Op: "in", Value: json.RawMessage(`["Drama"]`)}},
	}}
	f, err := CompileLibraryPolicies(movieRules, "show", showRules, "hide")
	if err != nil {
		t.Fatal(err)
	}
	frMovie := &core.Title{Type: core.TypeMovie, Language: "fr"}
	enMovie := &core.Title{Type: core.TypeMovie, Language: "en"}
	dramaShow := &core.Title{Type: core.TypeShow, Genres: []string{"Drama"}}
	comedyShow := &core.Title{Type: core.TypeShow, Genres: []string{"Comedy"}}

	if !f.Hidden(frMovie) || f.Hidden(enMovie) {
		t.Fatal("movie rules/default mismatch")
	}
	if f.Hidden(dramaShow) || !f.Hidden(comedyShow) {
		t.Fatal("show rules/default mismatch")
	}
	if hidden, idx, byFallback := f.MatchedHideRule(frMovie); !hidden || byFallback || f.RuleName(idx) != "Hide French Movies" {
		t.Fatalf("MatchedHideRule(frMovie) = %v, %d (%q), %v", hidden, idx, f.RuleName(idx), byFallback)
	}
	if hidden, _, byFallback := f.MatchedHideRule(comedyShow); !hidden || !byFallback {
		t.Fatalf("MatchedHideRule(comedyShow) = %v, byFallback=%v", hidden, byFallback)
	}
}
