//go:build windows

package catalog

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// mapFile maps path read-only. The file handle and the mapping handle are
// closed once the view exists; the view keeps the file open until it is
// unmapped, so the file is deleted only after unmapping (tail_loop.go).
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
	size := st.Size()
	if size == 0 {
		return nil, nil, errTailFile
	}
	h, err := windows.CreateFileMapping(windows.Handle(f.Fd()), nil, windows.PAGE_READONLY, uint32(size>>32), uint32(size), nil)
	if err != nil {
		return nil, nil, err
	}
	addr, err := windows.MapViewOfFile(h, windows.FILE_MAP_READ, 0, 0, uintptr(size))
	windows.CloseHandle(h)
	if err != nil {
		return nil, nil, err
	}
	// The view lives outside the Go heap, so reinterpret the uintptr's bits as
	// a pointer rather than converting it (the form go vet's unsafeptr accepts).
	data := unsafe.Slice((*byte)(*(*unsafe.Pointer)(unsafe.Pointer(&addr))), int(size))
	return data, func() error { return windows.UnmapViewOfFile(addr) }, nil
}
