package plexwatch

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// TestStepAsideExplainPartialShowNamesMissingEpisodes proves the owner sees
// which aired episodes their server lacks, in S01E05 form, rather than a bare
// "still showing".
func TestStepAsideExplainPartialShowNamesMissingEpisodes(t *testing.T) {
	h := newPMSHarness(t)
	_, srv := h.server("step-aside", 100, 0)
	// Ether's aired, regular list for the show: S1E1, S1E2, S2E1, S2E2.
	h.catalog.aired = map[core.RatingKey][]core.RatingKey{
		showS: {
			core.EpisodeKey(1399, 1, 1),
			core.EpisodeKey(1399, 1, 2),
			core.EpisodeKey(1399, 2, 1),
			core.EpisodeKey(1399, 2, 2),
		},
	}
	if _, err := h.svc.SetStepAside(h.ctx, srv.ID, true); err != nil {
		t.Fatal(err)
	}

	exp, err := h.svc.ExplainStepAside(h.ctx, showS)
	if err != nil {
		t.Fatal(err)
	}
	if exp.Hidden {
		t.Fatalf("partial show must not be hidden: %+v", exp)
	}
	if exp.Cause != StepAsideCauseMissingEpisodes {
		t.Fatalf("cause = %q, want %q", exp.Cause, StepAsideCauseMissingEpisodes)
	}
	if exp.AiredRegular != 4 || exp.HeldEpisodes != 1 {
		t.Fatalf("aired=%d held=%d, want 4 and 1", exp.AiredRegular, exp.HeldEpisodes)
	}
	want := []string{"S01E01", "S02E01", "S02E02"}
	if len(exp.Missing) != len(want) {
		t.Fatalf("missing = %v, want %v", exp.Missing, want)
	}
	for i := range want {
		if exp.Missing[i] != want[i] {
			t.Fatalf("missing = %v, want %v", exp.Missing, want)
		}
	}
	if len(exp.Servers) != 1 || exp.Servers[0] != srv.Name {
		t.Fatalf("servers = %v, want [%s]", exp.Servers, srv.Name)
	}
}

// TestStepAsideExplainMovieHeldAndNotHeld proves a held movie is reported hidden
// with the holding server named, and an unheld movie explains why it still shows.
func TestStepAsideExplainMovieHeldAndNotHeld(t *testing.T) {
	h := newPMSHarness(t)
	_, srv := h.server("step-aside", 100, 0)
	if _, err := h.svc.SetStepAside(h.ctx, srv.ID, true); err != nil {
		t.Fatal(err)
	}

	held, err := h.svc.ExplainStepAside(h.ctx, movieA)
	if err != nil {
		t.Fatal(err)
	}
	if !held.Hidden || held.Cause != StepAsideCauseHeld {
		t.Fatalf("held movie = %+v, want hidden with cause %q", held, StepAsideCauseHeld)
	}
	if held.Kind != "movie" || held.Title != "The 'Burbs" {
		t.Fatalf("movie meta = kind %q title %q", held.Kind, held.Title)
	}
	if len(held.Servers) != 1 || held.Servers[0] != srv.Name {
		t.Fatalf("servers = %v, want [%s]", held.Servers, srv.Name)
	}

	// movieC is in Ether's catalogue but not on the step-aside server.
	notHeld, err := h.svc.ExplainStepAside(h.ctx, movieC)
	if err != nil {
		t.Fatal(err)
	}
	if notHeld.Hidden || notHeld.Cause != StepAsideCauseNotHeld {
		t.Fatalf("unheld movie = %+v, want cause %q", notHeld, StepAsideCauseNotHeld)
	}
}

// TestStepAsideExplainNoEpisodeList proves a show whose aired list Ether has not
// loaded is explained as exactly that, not as "not held".
func TestStepAsideExplainNoEpisodeList(t *testing.T) {
	h := newPMSHarness(t)
	_, srv := h.server("step-aside", 100, 0)
	// No aired list seeded: the server holds an episode but Ether cannot judge
	// completeness.
	if _, err := h.svc.SetStepAside(h.ctx, srv.ID, true); err != nil {
		t.Fatal(err)
	}
	exp, err := h.svc.ExplainStepAside(h.ctx, showS)
	if err != nil {
		t.Fatal(err)
	}
	if exp.Hidden || exp.Cause != StepAsideCauseNoEpisodeList {
		t.Fatalf("explain = %+v, want cause %q", exp, StepAsideCauseNoEpisodeList)
	}
}
