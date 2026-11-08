//go:build unix

package catalog

import (
	"os"

	"golang.org/x/sys/unix"
)

// mapFile maps path read-only and shared: the pages are the file's page
// cache, clean and reclaimable, never the process heap.
func mapFile(path string) ([]byte, func() error, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if st.Size() == 0 {
		return nil, nil, errTailFile
	}
	data, err := unix.Mmap(int(f.Fd()), 0, int(st.Size()), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return readFile(path)
	}
	return data, func() error { return unix.Munmap(data) }, nil
}
