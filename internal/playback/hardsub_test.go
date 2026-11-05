package playback

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
	"time"
)

type subtitleResolver struct {
	*fakeResolver
	verdicts map[string]bool
}

func (r *subtitleResolver) SubtitleVerdict(_ context.Context, id string) (bool, bool) {
	v, ok := r.verdicts[id]
	return v, ok
}
func (r *subtitleResolver) RememberSubtitleVerdict(_ context.Context, id string, v bool) error {
	r.verdicts[id] = v
	return nil
}

type subtitleTranscoder struct {
	core.Transcoder
	calls int
}

func (t *subtitleTranscoder) DetectHardSubs(context.Context, string, time.Duration) (bool, bool) {
	t.calls++
	return t.calls == 1, true
}
func TestHardsubProbeFallback(t *testing.T) {
	for _, clean := range []bool{false, true} {
		t.Run(map[bool]string{false: "only-option", true: "clean-alternate"}[clean], func(t *testing.T) {
			rels := []*fakeRelease{mkv("hidden", 10000, time.Minute)}
			ids := []string{"hidden"}
			want := "hidden"
			if clean {
				rels = append(rels, mkv("clean", 10000, time.Minute))
				ids = append(ids, "clean")
				want = "clean"
			}
			h := newHarness(t, rels, ids)
			r := &subtitleResolver{h.resolver, map[string]bool{}}
			tc := &subtitleTranscoder{Transcoder: h.m.deps.Transcoder}
			h.m.deps.Resolver = r
			h.m.deps.Transcoder = tc
			ps := h.prepare(owner, movieKey, "c1")
			if ps.Resolution.Release.ID != want {
				t.Fatalf("selected %s, want %s", ps.Resolution.Release.ID, want)
			}
			if !r.verdicts["hidden"] {
				t.Fatal("content verdict not retained")
			}
			if h.resolver.isBad("hidden") {
				t.Fatal("last resort was permanently banned")
			}
		})
	}
}
