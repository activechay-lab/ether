package transcode

import (
	"log/slog"
	"sync"
	"time"
)

// The scheduler watchdog tells a host stall (the whole process descheduled,
// paged out or frozen) from an Ether stall (a lock, a channel, the encoder
// pipe). A goroutine that should wake every 50 ms records how late it woke;
// a live output that published late while the watchdog was equally late was
// not running at all. "host scheduler stalled" is a stable log key.
const (
	schedTick   = 50 * time.Millisecond
	schedReport = 250 * time.Millisecond
	schedWindow = 64 // samples kept: about three seconds
)

var sched struct {
	once   sync.Once
	mu     sync.Mutex
	at     [schedWindow]time.Time
	late   [schedWindow]time.Duration
	next   int
	warned time.Time
	logger *slog.Logger
}

// startSchedulerWatch starts the process-wide watchdog once.
func startSchedulerWatch(logger *slog.Logger) {
	sched.once.Do(func() {
		sched.logger = logger
		go func() {
			last := time.Now()
			for {
				time.Sleep(schedTick)
				now := time.Now()
				late := max(0, now.Sub(last)-schedTick)
				last = now
				sched.mu.Lock()
				sched.at[sched.next], sched.late[sched.next] = now, late
				sched.next = (sched.next + 1) % schedWindow
				warn := late >= schedReport && now.Sub(sched.warned) >= 10*time.Second
				if warn {
					sched.warned = now
				}
				sched.mu.Unlock()
				if warn && sched.logger != nil {
					sched.logger.Warn("host scheduler stalled", "late", late)
				}
			}
		}()
	})
}

// SchedulerLate is the watchdog's worst lateness within window (zero before
// it starts). Compare it with a pipeline delay of the same moment.
func SchedulerLate(window time.Duration) time.Duration {
	sched.mu.Lock()
	defer sched.mu.Unlock()
	worst := time.Duration(0)
	for i, at := range sched.at {
		if !at.IsZero() && time.Since(at) <= window {
			worst = max(worst, sched.late[i])
		}
	}
	return worst
}
