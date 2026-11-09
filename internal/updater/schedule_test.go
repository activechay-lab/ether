package updater

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// rolloutID returns an install identity whose rollout bucket lies in
// [lo, hi), so tests do not depend on one hash value.
func rolloutID(t *testing.T, lo, hi float64) string {
	t.Helper()
	for i := range 1000 {
		id := fmt.Sprintf("machine-%d", i)
		if b := RolloutBucket(id); b >= lo && b < hi {
			return id
		}
	}
	t.Fatal("no identity in bucket range")
	return ""
}

func TestRolloutBucketStable(t *testing.T) {
	a, b := RolloutBucket("machine-a"), RolloutBucket("machine-b")
	if a != RolloutBucket("machine-a") || a == b || a < 0 || a >= 1 || b < 0 || b >= 1 {
		t.Fatalf("buckets %v %v", a, b)
	}
	if RolloutBucket("") != 0 {
		t.Fatal("no identity must not wait")
	}
}

// A staged rollout holds the automatic install (and the download) until
// this server's share of RolloutHours has passed; "Update now" ignores it.
func TestRolloutGatesAutomaticInstallOnly(t *testing.T) {
	id := rolloutID(t, 0.4, 0.6)
	h := newHarness(t, func(o *ManagerOptions) { o.InstallID = id })
	h.cfg.MaintenanceStart, h.cfg.MaintenanceEnd = "00:00", "00:00" // always open
	pub := h.clock.Now()
	h.checker.upd.Manifest.PublishedAt, h.checker.upd.Manifest.RolloutHours = pub, 24
	due := pub.Add(time.Duration(RolloutBucket(id) * float64(24*time.Hour)))

	h.run(due.Sub(h.clock.Now()) - time.Minute)
	st := h.m.Status()
	if st.State != StateWaitingRollout || h.checker.downloads != 0 || len(h.applied) != 0 {
		t.Fatalf("before rollout: state=%s downloads=%d applied=%d", st.State, h.checker.downloads, len(h.applied))
	}
	if st.Available == nil || st.Available.InstallAfter == nil || !st.Available.InstallAfter.Equal(due) {
		t.Fatalf("installAfter = %+v, want %v", st.Available, due)
	}
	h.run(2 * time.Minute)
	if len(h.applied) != 1 {
		t.Fatalf("not installed once the rollout reached this server: state=%s", h.state())
	}

	m := newHarness(t, func(o *ManagerOptions) { o.InstallID = id })
	m.checker.upd.Manifest.PublishedAt, m.checker.upd.Manifest.RolloutHours = m.clock.Now(), 24
	if err := m.m.UpdateNow(true); err != nil {
		t.Fatal(err)
	}
	m.run(DefaultTick)
	if len(m.applied) != 1 {
		t.Fatalf("Update now waited for the rollout: state=%s", m.state())
	}
}

// A critical release skips rollout and window, but still waits until
// nobody has watched for the quiet period.
func TestCriticalInstallsAtNextQuietMoment(t *testing.T) {
	h := newHarness(t, func(o *ManagerOptions) { o.InstallID = rolloutID(t, 0.9, 1) })
	h.checker.upd.Manifest.PublishedAt, h.checker.upd.Manifest.RolloutHours = h.clock.Now(), 24
	h.checker.upd.Manifest.Critical = true
	h.active = 1
	h.run(2 * time.Hour) // 14:00, outside the 03:00–06:00 window
	if len(h.applied) != 0 || h.state() != StateWaitingIdle {
		t.Fatalf("installed while watching: state=%s", h.state())
	}
	h.active = 0
	h.run(DefaultQuietPeriod - time.Minute)
	if len(h.applied) != 0 {
		t.Fatal("installed before the quiet period")
	}
	h.run(2 * time.Minute)
	if len(h.applied) != 1 {
		t.Fatalf("critical update not installed once quiet: state=%s", h.state())
	}
}

// A server busy through MissedWindows maintenance windows installs at the
// next long quiet moment outside the window, never while anyone watches.
func TestMissedWindowsFallBackToLongQuietMoment(t *testing.T) {
	h := newHarness(t, nil)
	h.run(DefaultFirstCheckDelay)
	// A quiet afternoon before any window was missed: keeps waiting.
	h.run(time.Hour)
	if len(h.applied) != 0 || h.state() != StateWaitingWindow {
		t.Fatalf("installed outside the window too soon: state=%s", h.state())
	}
	// Busy through three nights (Jan 11, 12 and 13).
	h.active = 1
	h.run(time.Date(2026, 1, 13, 12, 0, 0, 0, time.UTC).Sub(h.clock.Now()))
	st := h.m.Status()
	if len(h.applied) != 0 || st.State != StateWaitingIdle || st.Available == nil || !st.Available.CatchUp {
		t.Fatalf("after three busy windows: state=%s available=%+v", st.State, st.Available)
	}
	h.active = 0
	h.run(DefaultCatchUpQuiet - time.Minute)
	if len(h.applied) != 0 {
		t.Fatal("installed before the long quiet moment")
	}
	h.run(2 * time.Minute)
	if len(h.applied) != 1 {
		t.Fatalf("catch-up install did not happen: state=%s", h.state())
	}
}

// A release the feed stops offering before it installs is dropped from
// disk and never installed.
func TestPulledReleaseDropsStagedDownload(t *testing.T) {
	h := newHarness(t, nil)
	h.run(DefaultFirstCheckDelay)
	staged := filepath.Join(h.dir, "ether-2.0.0")
	if _, err := os.Stat(staged); err != nil {
		t.Fatal("not staged")
	}
	h.checker.upd = nil
	h.run(DefaultPollInterval + time.Hour)
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatal("pulled release still staged")
	}
	h.run(15 * time.Hour) // through the window
	if len(h.applied) != 0 || h.m.Status().Available != nil {
		t.Fatal("pulled release installed")
	}
}

// Feed polls are spread over ±PollJitter, never below half the interval.
func TestPollJitterBounds(t *testing.T) {
	for _, r := range []float64{0, 0.5, 0.999999} {
		m := NewManager(ManagerOptions{Dir: t.TempDir(), Rand: func() float64 { return r }, Logger: quietLog()})
		d := m.pollDelay()
		if d < DefaultPollInterval-DefaultPollJitter || d > DefaultPollInterval+DefaultPollJitter {
			t.Fatalf("rand %v: delay %v", r, d)
		}
	}
}
