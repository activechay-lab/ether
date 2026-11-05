package resolver

import (
	"time"
)

// waitRankedValidation preserves rank order during grace. A healthy equivalent
// may win afterwards; an unhealthy backup never cuts the preferred check short.
// A consumed backup is restored for the ordinary ranked loop if preferred wins.
func waitRankedValidation(preferred, backup chan validation, grace time.Duration) validation {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	var ready *validation
	defer func() {
		if ready != nil {
			backup <- *ready
		}
	}()
	backupEvents := (<-chan validation)(backup)
	graceExpired := false
	for {
		// If both become ready together, keep the owner's preferred source.
		select {
		case v := <-preferred:
			return v
		default:
		}
		if graceExpired && ready != nil && ready.healthy && ready.err == nil {
			return *ready
		}
		select {
		case v := <-preferred:
			return v
		case v := <-backupEvents:
			ready = &v
			backupEvents = nil
		case <-timer.C:
			graceExpired = true
		}
	}
}
