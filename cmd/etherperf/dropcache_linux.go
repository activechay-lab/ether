package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/unix"
)

// runDropCache evicts every regular file under the given paths from the page
// cache (flushing each filesystem first, so dirty pages can go too) and reports how much
// was resident before and after. It runs inside a helper container that
// shares the measured server's mounts and image.
func runDropCache(args []string) error {
	var files, before, after int64
	for _, root := range args {
		// One flush per filesystem, so dirty pages can be evicted too.
		if fd, err := unix.Open(root, unix.O_RDONLY|unix.O_CLOEXEC, 0); err == nil {
			_ = unix.Syncfs(fd)
			unix.Close(fd)
		}
		err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
			if err != nil || !e.Type().IsRegular() {
				return nil
			}
			fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOATIME|unix.O_CLOEXEC, 0)
			if err != nil {
				if fd, err = unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0); err != nil {
					return nil
				}
			}
			defer unix.Close(fd)
			files++
			before += resident(fd)
			_ = unix.Fadvise(fd, 0, 0, unix.FADV_DONTNEED)
			after += resident(fd)
			return nil
		})
		if err != nil {
			return err
		}
	}
	fmt.Printf("dropcache: %d files, %d MB resident before, %d MB after\n", files, before>>20, after>>20)
	return nil
}

// resident is how many bytes of the open file are in the page cache.
func resident(fd int) int64 {
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Size == 0 {
		return 0
	}
	m, err := unix.Mmap(fd, 0, int(st.Size), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return 0
	}
	defer unix.Munmap(m)
	page := int64(os.Getpagesize())
	vec := make([]byte, (st.Size+page-1)/page)
	if _, _, errno := unix.Syscall(unix.SYS_MINCORE, uintptr(unsafe.Pointer(&m[0])), uintptr(len(m)), uintptr(unsafe.Pointer(&vec[0]))); errno != 0 {
		return 0
	}
	var n int64
	for _, v := range vec {
		if v&1 != 0 {
			n += page
		}
	}
	return n
}
