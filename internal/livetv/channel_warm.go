package livetv

import "time"

func channelWarmRefresh(p Programme, now time.Time) time.Duration {
	if !p.Start.After(now) && p.End.After(now) {
		return 15 * time.Second
	}
	return 10 * time.Minute
}
func maxTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return b
	}
	return a
}
