package atomicfile

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentReplacementPublishesWholeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	var wg sync.WaitGroup
	for i := byte(1); i <= 8; i++ {
		wg.Go(func() {
			if e := Write(path, bytes.Repeat([]byte{i}, 4096), 0600); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if len(b) != 4096 || !bytes.Equal(b, bytes.Repeat(b[:1], 4096)) {
		t.Fatal("partial replacement")
	}
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 1 {
		t.Fatalf("temporary files remain: %v %v", entries, e)
	}
}
