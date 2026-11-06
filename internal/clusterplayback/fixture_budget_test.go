package clusterplayback

import "time"

// Schema migration is fixture I/O, not execution time. On durable PostgreSQL
// its measured cost also supplies four fixture-times of headroom for the test's later SQL round trips.
// This changes only the enclosing harness deadline, never runtime lease,
// fencing, RPC or segment-wait bounds, nor any invariant assertion.
func fixtureExecutionBudget(start time.Time, minimum time.Duration) time.Duration {
	return max(minimum, 4*time.Since(start))
}
