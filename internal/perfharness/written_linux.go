package perfharness

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// processBytesWritten is what this process has caused to be written to
// storage (Linux /proc/self/io write_bytes), or -1.
func processBytesWritten() int64 {
	f, err := os.Open("/proc/self/io")
	if err != nil {
		return -1
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "write_bytes: "); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err == nil {
				return n
			}
		}
	}
	return -1
}
