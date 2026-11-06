package cluster

import (
	"sync"
	"time"
)

// ClockStatus is an observation against the shared database clock, not an
// authority token. Uncertainty includes half the measured round trip.
type ClockStatus struct {
	ClockSkew   time.Duration `json:"clockSkewNs"`
	RTT         time.Duration `json:"rttNs"`
	Uncertainty time.Duration `json:"uncertaintyNs"`
	Bound       time.Duration `json:"boundNs"`
	SampledAt   time.Time     `json:"sampledAt"`
	Ready       bool          `json:"ready"`
	Reason      string        `json:"reason,omitempty"`
	LastStep    time.Duration `json:"lastStepNs"`
}

type clockGuard struct {
	mu                  sync.Mutex
	status              ClockStatus
	known               bool
	good                int
	wall, mono, receipt time.Time
	maxAge              time.Duration
}

func absClock(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// observe detects steps independently of PostgreSQL availability. Wall samples
// explicitly discard monotonic data; mono samples retain it in production.
func (c *clockGuard) observe(wall, mono time.Time) time.Duration {
	var step time.Duration
	if !c.mono.IsZero() {
		step = wall.Round(0).Sub(c.wall.Round(0)) - mono.Sub(c.mono)
		if absClock(step) > max(time.Millisecond, min(10*time.Millisecond, c.status.Bound/10)) {
			c.status.LastStep = step
			c.status.Ready, c.good = false, 0
		} else {
			step = 0
		}
	}
	c.wall, c.mono = wall, mono
	return step
}

func (c *clockGuard) sample(before, after, monoBefore, monoAfter, database time.Time, ok bool) (ClockStatus, time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	step := c.observe(after, monoAfter)
	rtt := monoAfter.Sub(monoBefore)
	// A step during the query invalidates its midpoint too.
	queryStep := after.Round(0).Sub(before.Round(0)) - rtt
	if absClock(queryStep) > time.Millisecond {
		step = queryStep
		c.status.LastStep = step
	}
	if ok && rtt >= 0 {
		// Retain the latest estimate even when a step invalidates admission.
		// Use the after wall sample so diagnostics reflect the new clock.
		c.status.ClockSkew = after.Round(0).Add(-rtt / 2).Sub(database)
		c.status.RTT = rtt
		c.status.Uncertainty = absClock(c.status.ClockSkew) + rtt/2 + time.Microsecond
		c.status.SampledAt, c.receipt = after.Round(0).UTC(), monoAfter
	}
	if !ok || step != 0 || rtt < 0 {
		c.status.Ready, c.good = false, 0
	} else {
		// Each node gets half the pairwise budget against the common DB reference.
		limit := c.status.Bound / 2
		if !c.known || c.status.Uncertainty > limit {
			c.status.Ready, c.good = false, 0
		} else if !c.status.Ready {
			if c.status.Uncertainty <= limit*4/5 {
				c.good++
			} else {
				c.good = 0
			}
			c.status.Ready = c.good >= 2
		}
	}
	return c.snapshotLocked(monoAfter), step
}
func (c *clockGuard) snapshot(wall, mono time.Time) (ClockStatus, time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	step := c.observe(wall, mono)
	return c.snapshotLocked(mono), step
}
func (c *clockGuard) snapshotLocked(mono time.Time) ClockStatus {
	if c.receipt.IsZero() || mono.Sub(c.receipt) > c.maxAge {
		c.status.Ready, c.good = false, 0
	}
	s := c.status
	if !s.Ready {
		s.Reason = "clock_skew"
	} else {
		s.Reason = ""
	}
	return s
}

// ClockStatus also checks for local wall steps at each authority admission,
// closing the interval between heartbeat samples without a database round trip.
func (p *Postgres) ClockStatus() ClockStatus {
	s, step := p.clock.snapshot(p.o.Now(), p.o.MonotonicNow())
	p.logClockStep(step)
	return s
}
func (p *Postgres) ClockReady() bool { return p.ClockStatus().Ready }
func (p *Postgres) logClockStep(step time.Duration) {
	if step != 0 {
		p.log.Warn("cluster: local wall clock stepped", "step", step, "reason", "clock_skew")
	}
}
