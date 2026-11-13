package playback

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type runtimeRejectRecorder struct {
	core.Resolver
	rejected string
}

func (r *runtimeRejectRecorder) RejectRelease(_ context.Context, _ core.RatingKey, rel core.Release) error {
	r.rejected = rel.ID
	return nil
}

func TestWrongShowRuntimePersistedResume(t *testing.T) {
	const name = "Whos.the.Boss.S01E11.1080p.VIKI.WEB-DL.AAC2.0.H.264-FUZEER"
	duration := 45*time.Minute + 51*time.Second
	rels := func() []*fakeRelease {
		return []*fakeRelease{mkv("rel1", 100000, duration), mkv("right", 100000, 25*time.Minute)}
	}
	first, second, _, _ := restartHarnesses(t, rels, nil)
	// Seed the old server's remembered selection and persisted probe.
	first.resolver.candidates[0].Name = name
	first.resolver.candidates[0].TitleIDs = core.IDs{TVDB: 76587}
	first.catalog.titles[movieKey].Runtime = duration
	first.prepare(owner, movieKey, "old")
	if err := first.m.Close(); err != nil {
		t.Fatal(err)
	}
	title := &core.Title{Key: movieKey, Type: core.TypeEpisode, ShowTitle: "Who's the Boss?", Title: "Samantha's Growing Up", Season: 1, Episode: 11, ShowIDs: core.IDs{TVDB: 76587}, Runtime: 25 * time.Minute}
	second.catalog.titles[movieKey] = title
	second.resolver.candidates = append(second.resolver.candidates, core.Release{ID: "right", Name: "Whos.the.Boss.1984.S01E11"})
	recorder := &runtimeRejectRecorder{Resolver: second.resolver}
	second.m.deps.Resolver = recorder
	// prepare supplies no offset: the remembered release still must be checked.
	ps := second.prepare(owner, movieKey, "new")
	if ps.Resolution.Release.ID != "right" || recorder.rejected != "rel1" {
		t.Fatalf("played %s, rejected %s", ps.Resolution.Release.ID, recorder.rejected)
	}
	if second.tr.probes != 1 {
		t.Fatalf("probes = %d; persisted wrong probe should be reused", second.tr.probes)
	}
	for _, tc := range []struct {
		name     string
		duration time.Duration
		wrong    bool
	}{
		{name, duration, true},
		{"Show.S01E11-E12", 50 * time.Minute, false},
		{"Show.S01E11", 32 * time.Minute, false},
	} {
		if got := releaseRuntimeMismatch(title, core.Release{Name: tc.name}, tc.duration); got != tc.wrong {
			t.Errorf("%s: mismatch %v", tc.name, got)
		}
	}
}
