package app

import (
	"context"
	"time"
)

// quiesce waits, up to max, until the work the App does behind its
// responses is idle: catalogue refreshes and recounts behind reads, Plex
// Home screens rebuilt behind a response, viewer models rebuilt after a
// watch-state change (a debounced rebuild is started at once) and Xtream
// whole-library listings built behind a sign-in. The performance gate
// calls it before each measured request, so a request's work never depends
// on whether an earlier request's background work has finished. It reports
// whether everything went idle. Production never calls it.
func (a *App) quiesce(ctx context.Context, max time.Duration) bool {
	deadline := time.Now().Add(max)
	idle := 0
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if a.personalize != nil {
			a.personalize.FlushDebounced()
		}
		busy := 0
		if a.catalog != nil {
			busy += a.catalog.BackgroundBusy()
		}
		if a.plexServer != nil {
			busy += a.plexServer.BackgroundBusy()
		}
		if a.personalize != nil {
			busy += a.personalize.BackgroundBusy()
		}
		busy += a.frontends.backgroundBusy()
		if busy == 0 {
			// Twice in a row: one finished task may start the next.
			if idle++; idle >= 2 {
				return true
			}
		} else {
			idle = 0
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
