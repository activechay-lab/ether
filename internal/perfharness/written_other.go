//go:build !linux

package perfharness

// processBytesWritten is unavailable off Linux.
func processBytesWritten() int64 { return -1 }
