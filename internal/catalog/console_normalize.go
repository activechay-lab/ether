package catalog

import (
	"github.com/braedonsaunders/ether/internal/core"
	"reflect"
)

// normalizeConsoleFilter hoists metadata predicates out of mandatory AND
// groups so SQLite can plan the chip builder's wire shape like a flat filter.
// OR alternatives, viewer state and conflicting fields remain AND residuals.
// Neither the caller's AST nor its slices are mutated (cursor signatures bind
// the original expression at the API boundary).
func normalizeConsoleFilter(f core.Filter) core.Filter {
	if f.Group == nil {
		return f
	}
	out := f
	out.Group = nil
	target := reflect.ValueOf(&out).Elem()
	var residual []core.FilterGroup
	changed := false
	var walk func(*core.FilterGroup)
	walk = func(g *core.FilterGroup) {
		if g == nil {
			return
		}
		if groupHasOwnState(g) || g.ParentOnly {
			residual = append(residual, *g)
			return
		}
		if g.Filter != nil {
			if g.Filter.Group != nil {
				residual = append(residual, *g)
				return
			}
			source := reflect.ValueOf(*g.Filter)
			for i := 0; i < source.NumField(); i++ {
				value := source.Field(i)
				if !value.IsZero() && !target.Field(i).IsZero() && !reflect.DeepEqual(target.Field(i).Interface(), value.Interface()) {
					residual = append(residual, *g)
					return
				}
			}
			for i := 0; i < source.NumField(); i++ {
				if !source.Field(i).IsZero() {
					target.Field(i).Set(source.Field(i))
				}
			}
			changed = true
			return
		}
		if g.Operator != "and" {
			residual = append(residual, *g)
			return
		}
		for i := range g.Children {
			walk(&g.Children[i])
		}
	}
	walk(f.Group)
	if !changed {
		return f
	}
	if len(residual) == 1 {
		out.Group = &residual[0]
	} else if len(residual) > 1 {
		out.Group = &core.FilterGroup{Operator: "and", Children: residual}
	}
	return out
}
