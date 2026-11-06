package clusterplayback

import (
	"database/sql"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

var fixtureSQLSetupNanos atomic.Int64

func openFixtureDB(t *testing.T) *sql.DB {
	t.Helper()
	started := time.Now()
	db := store.OpenTest(t)
	elapsed := int64(time.Since(started))
	for old := fixtureSQLSetupNanos.Load(); elapsed > old; old = fixtureSQLSetupNanos.Load() {
		if fixtureSQLSetupNanos.CompareAndSwap(old, elapsed) {
			break
		}
	}
	return db
}

// Schema migration is fixture I/O, not execution time. Its measured cost also
// supplies headroom for later SQL round trips. No invariant assertion or
// production RPC, lease, fencing or segment-wait bound changes.
func fixtureExecutionBudget(start time.Time, minimum time.Duration) time.Duration {
	return max(minimum, 4*time.Since(start), 4*time.Duration(fixtureSQLSetupNanos.Load()))
}

// Placement transactions in these fixtures need durable commits too. Calibrate
// their test-only SQL deadline from observed schema cost, within placement's
// existing one-minute maximum. SQLite retains the original one-second minimum.
func fixtureSQLBudget() time.Duration {
	return min(time.Minute, max(time.Second, time.Duration(fixtureSQLSetupNanos.Load())/4))
}
