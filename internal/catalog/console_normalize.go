package catalog

import (
	"github.com/braedonsaunders/ether/internal/core"
	"reflect"
)

// normalizeConsoleFilter collapses the simple chip builder's conjunction to
// one predicate set. Repeating the full browse-policy scope in every leaf
// prevents efficient SQLite plans for otherwise identical flat filters.
// Conflicting fields, ORs and viewer state retain their original expression.
func normalizeConsoleFilter(f core.Filter) core.Filter {
	if f.Group == nil {
		return f
	}
	out := f
	out.Group = nil
	target := reflect.ValueOf(&out).Elem()
	var walk func(*core.FilterGroup) bool
	walk = func(g *core.FilterGroup) bool {
		if g == nil || groupHasOwnState(g) || g.ParentOnly {
			return false
		}
		if g.Filter != nil {
			if g.Filter.Group != nil {
				return false
			}
			source := reflect.ValueOf(*g.Filter)
			for i := 0; i < source.NumField(); i++ {
				value := source.Field(i)
				if value.IsZero() {
					continue
				}
				if !target.Field(i).IsZero() {
					return false
				}
				target.Field(i).Set(value)
			}
			return true
		}
		if g.Operator != "and" {
			return false
		}
		for i := range g.Children {
			if !walk(&g.Children[i]) {
				return false
			}
		}
		return true
	}
	if !walk(f.Group) {
		return f
	}
	return out
}
