//go:build linux

package hoststat

import "os"

func physicalMemory() uint64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	return ParseMemTotal(string(data))
}
