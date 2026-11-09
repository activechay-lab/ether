//go:build unix

package catalog

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"golang.org/x/sys/unix"
)

func TestTailMappingFailurePreservesIndex(t *testing.T) {
	c := universeCatalog(t, newUniverse(), config.Catalog{}, true)
	c.tail.dir = t.TempDir()
	if err := c.buildTail(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.installTail(nil) })
	old := c.TailStatus()
	oldPath := c.tail.path
	mmap := tailMmap
	tailMmap = func(int, int64, int, int, int) ([]byte, error) { return nil, unix.ENOMEM }
	t.Cleanup(func() { tailMmap = mmap })
	// The file is valid and readable. Falling back to ReadFile would succeed
	// here, allocating the entire index in heap instead of reporting ENOMEM.
	if _, _, err := openTailFile(oldPath); !errors.Is(err, unix.ENOMEM) {
		t.Fatalf("mapping failure hidden by heap fallback: %v", err)
	}
	if err := c.buildTail(context.Background()); !errors.Is(err, unix.ENOMEM) {
		t.Fatalf("replacement build: %v", err)
	}
	if st := c.TailStatus(); !st.Ready || st.Titles != old.Titles || c.tail.path != oldPath {
		t.Fatalf("failed replacement discarded current index: %+v", st)
	}
	// A fresh node has no usable index, but keeps the file for a later retry.
	b := universeCatalog(t, newUniverse(), config.Catalog{}, true)
	b.tail.dir = c.tail.dir
	b.loadNewestTail()
	if st := b.TailStatus(); st.Ready || st.LastError == "" {
		t.Fatalf("failed initial mapping status: %+v", st)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("valid index deleted after transient mapping failure: %v", err)
	}
}
