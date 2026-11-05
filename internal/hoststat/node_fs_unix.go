//go:build linux || darwin

package hoststat

import (
	"context"
	"golang.org/x/sys/unix"
	"time"
)

// Statfs performs one syscall per configured path, never a directory scan.
// Local scratch filesystems are expected; a hung remote mount can delay Run.
func nativeFilesystem(ctx context.Context, path string) (FilesystemCapacity, error) {
	if err := ctx.Err(); err != nil {
		return FilesystemCapacity{}, err
	}
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return FilesystemCapacity{}, err
	}
	if err := ctx.Err(); err != nil {
		return FilesystemCapacity{}, err
	}
	b := uint64(st.Bsize)
	if st.Bsize <= 0 || st.Blocks > ^uint64(0)/b || st.Bavail > st.Blocks {
		return FilesystemCapacity{}, nil
	}
	at := time.Now()
	return FilesystemCapacity{TotalBytes: observed(st.Blocks*b, at), AvailableBytes: observed(st.Bavail*b, at)}, nil
}
