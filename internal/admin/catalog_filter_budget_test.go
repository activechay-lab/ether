package admin

import (
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
)

func TestCatalogFilterBudgetsConditionsSeparatelyFromGroups(t *testing.T) {
	for _, count := range []int{30, 32, 33} {
		root := core.FilterGroup{Operator: "and"}
		for first := 0; first < count; first += 10 {
			group := core.FilterGroup{Operator: "or"}
			for i := first; i < min(first+10, count); i++ {
				group.Children = append(group.Children, core.FilterGroup{Filter: &core.Filter{YearFrom: 1990 + i}})
			}
			root.Children = append(root.Children, group)
		}
		raw, err := json.Marshal(core.Filter{Group: &root})
		if err != nil {
			t.Fatal(err)
		}
		var decoded core.Filter
		err = decodeCatalogFilter(string(raw), &decoded)
		if count <= 32 && err != nil {
			t.Fatalf("%d legitimate grouped chips rejected: %v", count, err)
		}
		if count > 32 && err == nil {
			t.Fatal("33 condition leaves accepted")
		}
	}
}
