package plexwatch

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// TestStepAsideStatusCountsAndState proves the owner console can show what the
// step-aside scan matched and whether it is done, from indexed counts alone.
func TestStepAsideStatusCountsAndState(t *testing.T) {
	h := newPMSHarness(t)
	_, srv := h.server("step-aside", 100, 0)
	if _, err := h.svc.SetStepAside(h.ctx, srv.ID, true); err != nil {
		t.Fatal(err)
	}

	got, err := h.svc.Server(h.ctx, srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.StepAsideStatus == nil {
		t.Fatal("step-aside status must be present when the switch is on")
	}
	st := got.StepAsideStatus
	if st.Movies != 2 {
		t.Fatalf("movies = %d, want 2", st.Movies)
	}
	if st.Episodes != 1 {
		t.Fatalf("episodes = %d, want 1", st.Episodes)
	}
	// The show holds an episode but Ether has no aired list seeded here, so it
	// is counted as partial.
	if st.ShowsPartial != 1 || st.ShowsFull != 0 {
		t.Fatalf("shows full=%d partial=%d, want 0 and 1", st.ShowsFull, st.ShowsPartial)
	}
	if st.State != StepAsideStateComplete {
		t.Fatalf("state = %q, want %q", st.State, StepAsideStateComplete)
	}
	if st.LastSynced == 0 {
		t.Fatal("lastSynced must be set after a completed scan")
	}

	// With the aired list seeded, the show is fully held once every aired
	// regular episode is on the server.
	h.catalog.aired = map[core.RatingKey][]core.RatingKey{
		showS: {episodeE},
	}
	got, err = h.svc.Server(h.ctx, srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.StepAsideStatus.ShowsFull != 1 || got.StepAsideStatus.ShowsPartial != 0 {
		t.Fatalf("after aired seed: full=%d partial=%d, want 1 and 0",
			got.StepAsideStatus.ShowsFull, got.StepAsideStatus.ShowsPartial)
	}

	// Turning step-aside off drops the status entirely.
	off, err := h.svc.SetStepAside(h.ctx, srv.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if off.StepAsideStatus != nil {
		t.Fatalf("status must be nil when step aside is off: %+v", off.StepAsideStatus)
	}
}
