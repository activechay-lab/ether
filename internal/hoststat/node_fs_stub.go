//go:build !linux && !darwin && !windows

package hoststat

import "context"

func nativeFilesystem(ctx context.Context, path string) (FilesystemCapacity, error) {
	return FilesystemCapacity{}, ctx.Err()
}
