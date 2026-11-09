package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/braedonsaunders/ether/internal/store"
)

// A logical (PostgreSQL) backup taken on a released Ether restores on this
// one: tables and columns later migrations dropped are skipped and renamed
// columns load under their new names. The dumps are each release's freshly
// migrated schema (every table header, seeded rows only), exported by that
// release's own ExportLogical.
func TestReleasedLogicalBackupsRestore(t *testing.T) {
	for _, release := range []string{"v0.3.0", "v0.3.5"} {
		t.Run(release, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", "logical", release+".jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := store.ImportLogical(context.Background(), store.OpenTest(t), f); err != nil {
				t.Fatal(err)
			}
		})
	}
}
