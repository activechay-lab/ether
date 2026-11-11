package hoststat

import (
	"log/slog"
	"runtime/debug"
	"runtime/metrics"
	"time"
)

// The soft memory limit paces the garbage collector. When the live heap
// itself grows close to the limit, the collector runs back to back and can
// take most of the machine's CPU while freeing almost nothing: a user saw
// ~750% CPU on 8 cores, almost all of it GC mark work, under a GOMEMLIMIT
// set just above a heap that had grown through the day. The guard watches
// the collector's CPU share and, for a limit Ether chose itself, raises the
// limit (never past the container or RAM ceiling) so memory pressure costs
// memory instead of every core. A GOMEMLIMIT from the environment is never
// changed; the guard only says what it sees.

const (
	// guardInterval is how often the guard samples the runtime.
	guardInterval = 30 * time.Second
	// thrashGCShare is the share of all CPU time spent in GC over one
	// interval that counts as thrashing. Healthy Ether runs well under 5%.
	thrashGCShare = 0.25
	// thrashLiveShare: the live heap must be at least this share of the
	// limit for GC load to be the limit's fault rather than allocation churn.
	thrashLiveShare = 0.75
)

// gcSample is one reading of the runtime's cumulative CPU counters and
// the live heap after the last collection.
type gcSample struct {
	gcCPU, totalCPU float64 // cumulative CPU-seconds
	live            uint64  // bytes
}

// guardDecision reports whether the interval between prev and cur was GC
// thrashing against limit, and the limit to raise to (0 = leave it). The
// raise gives the live heap room to double, at least 1.5x the old limit,
// capped at ceiling; ceiling 0 means unknown, so nothing is raised.
func guardDecision(prev, cur gcSample, limit, ceiling int64) (next int64, thrashing bool) {
	dt := cur.totalCPU - prev.totalCPU
	if limit <= 0 || dt <= 0 {
		return 0, false
	}
	share := (cur.gcCPU - prev.gcCPU) / dt
	if share < thrashGCShare || float64(cur.live) < thrashLiveShare*float64(limit) {
		return 0, false
	}
	if ceiling <= limit {
		return 0, true
	}
	next = max(limit+limit/2, int64(cur.live)*2)
	return min(next, ceiling), true
}

// memoryCeiling is the most the guard may raise an automatic limit to:
// 95% of a container limit, else RAM less the OS reserve, else unknown.
func memoryCeiling(cgroupLimit, physicalRAM uint64) int64 {
	if cgroupLimit > 0 && cgroupLimit < unlimitedSentinel {
		return int64(cgroupLimit - cgroupLimit/20)
	}
	if physicalRAM > ramReserve {
		return int64(physicalRAM - ramReserve)
	}
	return 0
}

var guardMetrics = []string{
	"/cpu/classes/gc/total:cpu-seconds",
	"/cpu/classes/total:cpu-seconds",
	"/gc/heap/live:bytes",
}

func readGCSample() gcSample {
	s := make([]metrics.Sample, len(guardMetrics))
	for i, name := range guardMetrics {
		s[i].Name = name
	}
	metrics.Read(s)
	var out gcSample
	if s[0].Value.Kind() == metrics.KindFloat64 {
		out.gcCPU = s[0].Value.Float64()
	}
	if s[1].Value.Kind() == metrics.KindFloat64 {
		out.totalCPU = s[1].Value.Float64()
	}
	if s[2].Value.Kind() == metrics.KindUint64 {
		out.live = s[2].Value.Uint64()
	}
	return out
}

// watchMemoryLimit runs for the life of the process. adjustable is false
// when the limit came from GOMEMLIMIT: the guard then only warns, once.
func watchMemoryLimit(log *slog.Logger, adjustable bool, ceiling int64) {
	prev := readGCSample()
	warned := false
	t := time.NewTicker(guardInterval)
	defer t.Stop()
	for range t.C {
		cur := readGCSample()
		limit := debug.SetMemoryLimit(-1)
		bound := ceiling
		if !adjustable {
			bound = 0
		}
		next, thrashing := guardDecision(prev, cur, limit, bound)
		prev = cur
		switch {
		case !thrashing:
		case next > 0:
			debug.SetMemoryLimit(next)
			log.Warn("memory limit raised: the garbage collector was using most of the CPU because the live heap is close to the limit",
				"live_mib", cur.live>>20, "old_limit_mib", limit>>20, "new_limit_mib", next>>20, "ceiling_mib", ceiling>>20)
		case !warned:
			warned = true
			hint := "the limit is already at the container/RAM ceiling"
			if !adjustable {
				hint = "GOMEMLIMIT is set in the environment; raise or remove it"
			}
			log.Warn("garbage collector is using most of the CPU because the live heap is close to the memory limit",
				"live_mib", cur.live>>20, "limit_mib", limit>>20, "hint", hint)
		}
	}
}
