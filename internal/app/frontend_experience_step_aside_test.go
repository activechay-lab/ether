package app

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// TestSurfaceStepAsideDisablesUnrestrictedFastPath proves the shared surface
// disables owner/unrestricted fast-paths while step-aside holds titles, so
// Plex owners still run filtered selection instead of bypassing step-aside.
func TestSurfaceStepAsideDisablesUnrestrictedFastPath(t *testing.T) {
	owner := &core.User{ID: 1, Username: "owner"}
	unrestricted := func(*core.User) bool { return true }

	off := &surfaceExperience{Service: &experience.Service{}, protocol: "plex", rowGen: func() uint64 { return 0 }}
	if r, ok := off.NewRecent(frontend.RecentOptions{Unrestricted: unrestricted}).(experience.Recent); !ok || !r.Unrestricted(owner) {
		t.Fatal("step-aside off: owner fast-path should stay unrestricted")
	}

	on := &surfaceExperience{Service: &experience.Service{}, protocol: "plex", rowGen: func() uint64 { return 5 }}
	if r, ok := on.NewRecent(frontend.RecentOptions{Unrestricted: unrestricted}).(experience.Recent); !ok || r.Unrestricted(owner) {
		t.Fatal("step-aside on: owner fast-path still unrestricted")
	}
	if c, ok := on.NewContinue(frontend.ContinueOptions{Unrestricted: unrestricted}).(experience.Continue); !ok || c.Unrestricted(owner) {
		t.Fatal("step-aside on: continue fast-path still unrestricted")
	}
	if h, ok := on.NewHomeSelection(frontend.HomeSelectionOptions{}).(experience.HomeSelection); !ok || !h.Restricted {
		t.Fatal("step-aside on: home selection not restricted")
	}
	if p, ok := on.NewPostPlaySelection(frontend.PostPlaySelectionOptions{Unrestricted: true}).(experience.PostPlaySelection); !ok || p.Unrestricted {
		t.Fatal("step-aside on: post-play fast-path still unrestricted")
	}
}
