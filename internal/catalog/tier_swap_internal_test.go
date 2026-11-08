package catalog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestTierPinnedIndexesCoverIndexedBy: every catalog_titles index a query
// names in INDEXED BY must be built under its own name by the tier swap,
// or that query fails between the swap and the index's rename.
func TestTierPinnedIndexesCoverIndexedBy(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	direct := regexp.MustCompile(`INDEXED BY ([a-z0-9_]+)`)
	helper := regexp.MustCompile(`sqliteIndexedBy\([^,]+,\s*"([a-z0-9_]+)"\)`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, re := range []*regexp.Regexp{direct, helper} {
			for _, m := range re.FindAllStringSubmatch(string(src), -1) {
				if strings.HasPrefix(m[1], "catalog_titles") && !tierPinnedIndexes[m[1]] {
					t.Errorf("%s names catalog_titles index %s in INDEXED BY; add it to tierPinnedIndexes", f, m[1])
				}
			}
		}
	}
	if !tierPinnedIndexes[keysetIndex] {
		t.Errorf("keysetIndex %s is not pinned", keysetIndex)
	}
}
