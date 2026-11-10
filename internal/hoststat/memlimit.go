package hoststat

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
)

// Memory limit sources, in the order they win. Logged once at startup.
const (
	LimitSourceEnv      = "GOMEMLIMIT environment variable"
	LimitSourceCgroupV2 = "cgroup v2 memory.max"
	LimitSourceCgroupV1 = "cgroup v1 memory.limit_in_bytes"
	LimitSourceRAM      = "physical RAM"
	LimitSourceNone     = "none (memory size unknown)"
)

const (
	// containerShareDen: the limit is 9/10 of a container limit,
	// leaving headroom for stacks, the SQLite page cache and other
	// off-heap memory the Go soft limit does not count, so the cgroup
	// OOM killer stays out of reach.
	containerShareDen = 10
	// ramFloor keeps a tiny machine from GC-thrashing: the soft limit
	// only paces collection, so a floor above half of a very small
	// RAM is safe and avoids pathological collection when the live
	// heap alone approaches it.
	ramFloor = 1 << 30
	// ramReserve is left for the OS and everything else on the box.
	ramReserve = 1 << 30
	// unlimitedSentinel: cgroup v1 reports an unlimited limit as a
	// page-rounded value near 2^63; anything this large is not a cap.
	unlimitedSentinel = 1 << 62
)

// MemoryLimit computes the Go soft memory limit to install. A zero limit
// means "do not call SetMemoryLimit" (the environment already set one, or
// the machine size is unknown); source says why. Pure: inputs are the
// GOMEMLIMIT environment value, the container limit in bytes (0 = none)
// and physical RAM in bytes (0 = unknown).
func MemoryLimit(goMemLimitEnv string, cgroupLimit, physicalRAM uint64) (limit int64, source string) {
	return memoryLimit(goMemLimitEnv, cgroupLimit, physicalRAM, "")
}

func memoryLimit(goMemLimitEnv string, cgroupLimit, physicalRAM uint64, cgroupSource string) (int64, string) {
	if strings.TrimSpace(goMemLimitEnv) != "" {
		// The runtime already applied it; env always wins.
		return 0, LimitSourceEnv
	}
	if cgroupLimit > 0 && cgroupLimit < unlimitedSentinel {
		if cgroupSource == "" {
			cgroupSource = LimitSourceCgroupV2
		}
		// 90% without overflow for limits near the sentinel.
		return int64(cgroupLimit - cgroupLimit/containerShareDen), cgroupSource
	}
	if physicalRAM > 0 {
		// Half of RAM, but never so much that the OS is starved, and
		// never below the floor. On a big box (128 GiB) this is far
		// above Ether's steady heap, so behaviour there is unchanged;
		// on an 8 GiB box it caps the heap target at 4 GiB instead of
		// letting it drift to the machine's edge.
		limit := physicalRAM / 2
		if physicalRAM > ramReserve && physicalRAM-ramReserve < limit {
			limit = physicalRAM - ramReserve
		}
		if limit < ramFloor {
			limit = ramFloor
		}
		return int64(limit), LimitSourceRAM
	}
	return 0, LimitSourceNone
}

// ParseCgroupV2 reads a memory.max value: "max" or a byte count.
func ParseCgroupV2(content string) (uint64, bool) {
	s := strings.TrimSpace(content)
	if s == "" || s == "max" {
		return 0, false
	}
	return parseCgroupBytes(s)
}

// ParseCgroupV1 reads a memory.limit_in_bytes value, rejecting the
// near-2^63 sentinel an unlimited v1 cgroup reports.
func ParseCgroupV1(content string) (uint64, bool) {
	return parseCgroupBytes(strings.TrimSpace(content))
}

func parseCgroupBytes(s string) (uint64, bool) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n == 0 || n >= unlimitedSentinel {
		return 0, false
	}
	return n, true
}

// CgroupLimit reads the container memory limit under root (tests pass a
// fixture root; production passes ""). v2 wins over v1.
func CgroupLimit(root string) (uint64, string) {
	if root == "" {
		root = "/"
	}
	if b, err := os.ReadFile(filepath.Join(root, "sys/fs/cgroup/memory.max")); err == nil {
		if n, ok := ParseCgroupV2(string(b)); ok {
			return n, LimitSourceCgroupV2
		} else {
			// The file exists: this is a v2 hierarchy and it is
			// explicitly unlimited. Do not fall through to v1.
			return 0, ""
		}
	}
	if b, err := os.ReadFile(filepath.Join(root, "sys/fs/cgroup/memory/memory.limit_in_bytes")); err == nil {
		if n, ok := ParseCgroupV1(string(b)); ok {
			return n, LimitSourceCgroupV1
		}
	}
	return 0, ""
}

// PhysicalMemory returns total physical RAM in bytes, or 0 when the
// platform cannot say. Implemented per-OS in pure Go.
func PhysicalMemory() uint64 { return physicalMemory() }

// ParseMemTotal extracts MemTotal (KiB in /proc/meminfo) as bytes.
func ParseMemTotal(meminfo string) uint64 {
	for _, line := range strings.Split(meminfo, "\n") {
		key, rest, ok := strings.Cut(line, ":")
		if !ok || key != "MemTotal" {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return 0
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}

var applyMemoryLimitOnce sync.Once

// ApplyMemoryLimit installs the soft limit once per process and logs the
// choice. It never overrides an explicit GOMEMLIMIT.
func ApplyMemoryLimit(log *slog.Logger) {
	applyMemoryLimitOnce.Do(func() {
		if log == nil {
			log = slog.Default()
		}
		env := os.Getenv("GOMEMLIMIT")
		cgroup, cgroupSource := CgroupLimit("")
		ram := PhysicalMemory()
		limit, source := memoryLimit(env, cgroup, ram, cgroupSource)
		switch {
		case source == LimitSourceEnv:
			log.Info("memory limit: GOMEMLIMIT from the environment is in effect; not overriding", "gomemlimit", env)
		case limit <= 0:
			log.Warn("memory limit: could not determine container limit or physical RAM; leaving the Go default (set GOMEMLIMIT to bound the heap)")
		default:
			debug.SetMemoryLimit(limit)
			log.Info("memory limit set", "limit_bytes", limit, "limit_mib", limit>>20, "source", source,
				"cgroup_limit_bytes", cgroup, "physical_ram_bytes", ram)
		}
	})
}
