//go:build windows

package hoststat

import (
	"context"
	"golang.org/x/sys/windows"
	"time"
)

func nativeFilesystem(ctx context.Context, path string) (FilesystemCapacity, error) {
	if err := ctx.Err(); err != nil {
		return FilesystemCapacity{}, err
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return FilesystemCapacity{}, err
	}
	var available, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &available, &total, &free); err != nil {
		return FilesystemCapacity{}, err
	}
	if err := ctx.Err(); err != nil {
		return FilesystemCapacity{}, err
	}
	if available > total {
		return FilesystemCapacity{}, nil
	}
	at := time.Now()
	return FilesystemCapacity{TotalBytes: observed(total, at), AvailableBytes: observed(available, at)}, nil
}
