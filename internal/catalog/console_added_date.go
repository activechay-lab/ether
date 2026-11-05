package catalog

import (
	"github.com/braedonsaunders/ether/internal/core"
	"strings"
)

// Added follows release/air time. The series projection is maintained as air
// dates arrive, so this filter never walks a show's episode catalogue.
func consoleAddedDateConditions(f core.Filter, now int64) ([]string, []any) {
	if f.DateAddedFrom == 0 && f.DateAddedTo == 0 {
		return nil, nil
	}
	types := []core.MediaType{f.Type}
	if f.Type == "" {
		types = []core.MediaType{core.TypeMovie, core.TypeShow}
	}
	var choices []string
	var args []any
	for _, typ := range types {
		column := "t.released_ts"
		if typ == core.TypeShow {
			column = "t.show_added_ts"
		}
		clause := "(t.type=? AND " + column + ">0 AND " + column + "<=?"
		args = append(args, string(typ), now)
		if f.DateAddedFrom > 0 {
			clause += " AND " + column + ">=?"
			args = append(args, f.DateAddedFrom)
		}
		if f.DateAddedTo > 0 {
			clause += " AND " + column + "<=?"
			args = append(args, f.DateAddedTo)
		}
		choices = append(choices, clause+")")
	}
	return []string{"(" + strings.Join(choices, " OR ") + ")"}, args
}
