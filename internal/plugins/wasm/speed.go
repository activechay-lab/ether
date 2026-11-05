package wasm

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"time"
)

// The hot-path budgets (SPEC 5.2) are amounts of work, written as the time
// that work takes this engine on a reference machine (an Apple M-series
// core, native build). The same plugin doing the same work takes several
// times longer on a slower CPU, and several times longer again in a build
// with the race detector, which instruments the Go code wazero returns to
// at every loop of the guest. Holding every machine to the reference
// machine's milliseconds would drop a plugin's ranking on slow hardware
// however light the plugin is, and would make the race build's tests
// depend on the runner. So the engine measures its own speed once per
// process on a fixed workload, by the thread's CPU time (contention from
// other work does not count; the best of several runs is kept), and
// scales the hot-path budgets and the kill deadline by how much slower
// than the reference it is. It never scales below 1 or above
// maxHotScale.
const (
	// referenceHotWork is measureHotWork's result on the reference machine
	// (6.2-6.4 ms native; 18.5 ms on the same machine with -race).
	referenceHotWork = 6 * time.Millisecond
	maxHotScale      = 16
)

const calibrationJS = `globalThis.rank_key = function (c) { let s = 0; for (let i = 0; i < 40; i++) s += i * c.n; return s & 7; };`

var hotSpeed struct {
	once   sync.Once
	scale  float64
	sample time.Duration
}

// HotScale is how many times slower than the reference machine this
// process runs hosted plugin code (1 on the reference machine or faster).
// The first call measures it; it costs a few milliseconds and runs off the
// hot path, at plugin load.
func HotScale() float64 {
	hotSpeed.once.Do(func() {
		hotSpeed.sample = measureHotWork()
		hotSpeed.scale = hotScaleFor(hotSpeed.sample)
	})
	return hotSpeed.scale
}

func hotScaleFor(sample time.Duration) float64 {
	if sample <= 0 {
		return 1
	}
	s := float64(sample) / float64(referenceHotWork)
	return min(max(s, 1), maxHotScale)
}

// measureHotWork runs calibrationJS over a batch of 200 releases on a hot
// instance, the path a ranking batch takes (hand-over and ranking), and
// returns the best of five runs: the thread's CPU time where the platform
// has a CPU clock, wall time otherwise. 0 means the measurement failed.
func measureHotWork() time.Duration {
	ensureQJS()
	lim := Limits{}
	lim.normalize()
	in, err := newJSInstance(context.Background(), calibrationJS, &lim, true, nil, "calibration")
	if err != nil {
		return 0
	}
	defer in.closeInstance()
	facts := make([][]byte, 200)
	for i := range facts {
		facts[i] = json.RawMessage(fmt.Sprintf(`{"releaseId":"%d","name":"Calibration.Release.2020.1080p.WEB-DL.DDP5.1.H.264-GRP","languages":["english"],"n":%d}`, i, i))
	}
	unbounded := Limits{RankPerCandidate: 1 << 60, RankBatch: 1 << 60}
	cs := &callState{ctx: context.Background(), lim: &lim, hot: true}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var best time.Duration
	for range 5 {
		c0, cpuOK := threadCPU()
		w0 := time.Now()
		if _, err := in.rankBatch(context.Background(), cs, facts, []byte("{}"), &unbounded); err != nil {
			return 0
		}
		took := time.Since(w0)
		if c1, ok := threadCPU(); cpuOK && ok {
			took = c1 - c0
		}
		if best == 0 || took < best {
			best = took
		}
	}
	return best
}

// scaleHot stretches the hot-path budgets by the engine's measured speed.
func (l *Limits) scaleHot(scale float64) {
	l.RankPerCandidate = time.Duration(float64(l.RankPerCandidate) * scale)
	l.RankBatch = time.Duration(float64(l.RankBatch) * scale)
}

// scaleHot applies the measured speed to a plugin's limits, logging the
// measurement with the first plugin that needs it. Test limits that lift
// the clamp are left alone.
func (e *Engine) scaleHot(l *Limits) {
	if l.unclamped {
		return
	}
	scale := HotScale()
	e.speedLog.Do(func() {
		e.logger.Info("hosted plugin engine speed measured", "reference_ms", referenceHotWork.Milliseconds(),
			"measured", hotSpeed.sample, "budget_scale", scale)
	})
	l.scaleHot(scale)
}
