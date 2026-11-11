package playback

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// namesakeResolver makes the harness's catalog titles look ambiguous (a
// namesake exists) and reports a conflicting identifier for a chosen release.
type namesakeResolver struct {
	*fakeResolver
	mu         sync.Mutex
	ambiguous  bool
	conflicts  map[string]bool
	namesakeCh chan struct{}
}

func (r *namesakeResolver) NamesakeTitles(context.Context, *core.Title) bool { return r.ambiguous }

func (r *namesakeResolver) NamesakeConflict(_ context.Context, _ *core.Title, rel core.Release) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.conflicts[rel.ID]
}

// TestNamesakeRuntimeRejectsWrongFilm is the lasting invariant: two namesake
// 2026 movies share a normalized title and year, a release is named for one
// but its probed duration belongs to the other, so it is rejected and the
// next candidate plays.
func TestNamesakeRuntimeRejectsWrongFilm(t *testing.T) {
	// "The Runner" (catalogue runtime 98m); the release holds an 85m file.
	catalogue := 98 * time.Minute
	probed := 85 * time.Minute
	h := newHarness(t, []*fakeRelease{mkv("wrong", 100_000, probed), mkv("right", 100_000, catalogue)}, []string{"wrong", "right"})
	h.catalog.titles[movieKey] = &core.Title{Key: movieKey, Type: core.TypeMovie, Title: "Runner", Year: 2026, Runtime: catalogue}
	r := &namesakeResolver{fakeResolver: h.resolver, ambiguous: true}
	h.m.deps.Resolver = r

	ps := h.prepare(owner, movieKey, "c1")
	if got := ps.Info.Duration; got != catalogue {
		t.Fatalf("played duration = %v, want %v (namesake with the wrong film played)", got, catalogue)
	}
	if reason, ok := h.resolver.bad["wrong"]; !ok || reason != "runtime doesn't match this title" {
		t.Fatalf("bad reason = %q, want runtime mismatch", reason)
	}
	if h.resolver.isBad("right") {
		t.Fatal("correct release was blocked")
	}
}

// TestNamesakeNonAmbiguousKeepsExtendedCut proves the gate is namesake-only:
// an unambiguous title keeps a probed duration that differs from catalogue.
func TestNamesakeNonAmbiguousKeepsExtendedCut(t *testing.T) {
	catalogue := 98 * time.Minute
	probed := 130 * time.Minute // an extended cut
	h := newHarness(t, []*fakeRelease{mkv("extended", 100_000, probed)}, []string{"extended"})
	h.catalog.titles[movieKey] = &core.Title{Key: movieKey, Type: core.TypeMovie, Title: "Runner", Year: 2026, Runtime: catalogue}
	r := &namesakeResolver{fakeResolver: h.resolver, ambiguous: false}
	h.m.deps.Resolver = r

	ps := h.prepare(owner, movieKey, "c1")
	if got := ps.Info.Duration; got != probed {
		t.Fatalf("played duration = %v, want %v (extended cut should play)", got, probed)
	}
	if h.resolver.isBad("extended") {
		t.Fatal("extended cut of an unambiguous title was blocked")
	}
}

// TestNamesakeIdentifierConflictSkipsRelease rejects a release whose reported
// identifiers name the other title before it is opened.
func TestNamesakeIdentifierConflictSkipsRelease(t *testing.T) {
	// Both carry the catalogue runtime so the duration gate leaves them alone.
	h := newHarness(t, []*fakeRelease{mkv("other", 100_000, 98*time.Minute), mkv("this", 100_000, 98*time.Minute)}, []string{"other", "this"})
	h.catalog.titles[movieKey] = &core.Title{Key: movieKey, Type: core.TypeMovie, Title: "Runner", Year: 2026, Runtime: 98 * time.Minute}
	r := &namesakeResolver{fakeResolver: h.resolver, ambiguous: true, conflicts: map[string]bool{"other": true}}
	h.m.deps.Resolver = r

	ps := h.prepare(owner, movieKey, "c1")
	if h.m.lookup(ps.ID) == nil {
		t.Fatal("no session")
	}
	if h.tr.probes != 1 {
		// Only the surviving release was opened for probing.
		t.Fatalf("probes = %d, want 1 (the conflicting release should be skipped)", h.tr.probes)
	}
	if reason, ok := h.resolver.bad["other"]; !ok || reason != "release names the other title" {
		t.Fatalf("bad reason = %q, want identifier conflict", reason)
	}
}

// TestNamesakeDurationTolerance checks the tolerance bound in isolation.
func TestNamesakeDurationTolerance(t *testing.T) {
	cases := []struct {
		name     string
		runtime  time.Duration
		probed   time.Duration
		mismatch bool
	}{
		{"exact", 98 * time.Minute, 98 * time.Minute, false},
		{"within 8m slack short", 98 * time.Minute, 91 * time.Minute, false},
		{"beyond 8m slack short", 98 * time.Minute, 85 * time.Minute, true},
		{"within ratio long", 200 * time.Minute, 220 * time.Minute, false},
		{"beyond ratio long", 200 * time.Minute, 230 * time.Minute, true},
		{"unknown probe", 98 * time.Minute, 0, false},
		{"unknown runtime", 0, 85 * time.Minute, false},
	}
	for _, c := range cases {
		if got := namesakeDurationMismatch(c.runtime, c.probed); got != c.mismatch {
			t.Errorf("%s: mismatch = %v, want %v", c.name, got, c.mismatch)
		}
	}
}
