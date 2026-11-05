package catalog

import (
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// The Postgres driver binds Go bools as 0/1 for BIGINT flag columns. A bare
// `AND ?` makes Postgres infer boolean and rejects that integer, so no
// browse parameter may be a bool and the flag conditions must compare =1.
func TestBrowseWhereBindsFlagsNumerically(t *testing.T) {
	c := newChunkTestCatalog(t)
	for _, f := range []core.Filter{
		{Type: core.TypeShow},
		{Type: core.TypeMovie, DigitalDiscovery: true, DigitalDateEvidence: true},
	} {
		q, args, err := c.browseWhere(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, a := range args {
			if _, ok := a.(bool); ok {
				t.Fatalf("filter %+v: arg %d is bool in %s", f, i, q)
			}
		}
		if strings.Contains(q, "AND ?)") || strings.Contains(q, "AND ? ") {
			t.Fatalf("filter %+v leaves a bare boolean parameter: %s", f, q)
		}
	}
}
