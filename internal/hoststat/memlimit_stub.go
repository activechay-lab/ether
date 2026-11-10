//go:build !linux && !darwin && !windows

package hoststat

func physicalMemory() uint64 { return 0 }
