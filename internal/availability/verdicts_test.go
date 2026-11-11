package availability

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/resolver"
)

type recentMovies struct{ *fakeCatalog }

func (c recentMovies) RecentMovies(_ context.Context, since time.Time) ([]core.Title, error) {
	var out []core.Title
	for _, t := range c.titles {
		if t.Type == core.TypeMovie && t.Released.After(since) {
			out = append(out, t)
		}
	}
	return out, nil
}

// countingValidator is the real resolver, counting every release it judges.
type countingValidator struct {
	*resolver.Service
	judged int
}

func (c *countingValidator) FeedCandidate(ctx context.Context, t *core.Title, r core.Release) (core.Release, string, error) {
	c.judged++
	return c.Service.FeedCandidate(ctx, t, r)
}

func (c *countingValidator) FeedEvaluation(ctx context.Context) (core.FeedEvaluation, error) {
	ev, err := c.Service.FeedEvaluation(ctx)
	if err != nil {
		return nil, err
	}
	return countedEvaluation{ev, c}, nil
}

type countedEvaluation struct {
	core.FeedEvaluation
	c *countingValidator
}

func (e countedEvaluation) Evaluate(ctx context.Context, t *core.Title, rs []core.Release) ([]core.FeedVerdict, error) {
	e.c.judged += len(rs)
	return e.FeedEvaluation.Evaluate(ctx, t, rs)
}

// The #303 invariant: once a release has a durable verdict under the
// current policy, section and home loads judge no feed release, even after
// a restart, an observation or the hidden set expiring. A policy change
// judges again and its answer is exact.
func TestSectionLoadsJudgeNoFeedReleaseOnceVerdictsExist(t *testing.T) {
	x := setup(t)
	ctx := t.Context()
	r := resolver.New(nil, nil, nil, x.c, resolver.Options{})
	defer r.Close()
	v := &countingValidator{Service: r}
	x.s.d.Validator, x.s.d.Catalog = v, recentMovies{x.cat}
	movie := x.cat.titles[core.MovieKey(1)]
	movie.HomeReleased = x.now.Add(-time.Hour)
	x.cat.titles[movie.Key] = movie
	feed := []core.Release{
		{ID: "a", Name: "Film.2020.1080p.WEB-DL.DDP5.1.H.264-GRP", Indexer: "Fake", Size: 6 << 30},
		{ID: "b", Name: "Film.2020.720p.WEB-DL.DDP5.1.H.264-GRP", Indexer: "Fake", Size: 3 << 30},
		{ID: "c", Name: "Film.2020.CAM.x264-OTHER", Indexer: "Fake", Size: 1 << 30},
	}
	if err := x.s.retainFeed(ctx, x.f.sources[0], feed); err != nil {
		t.Fatal(err)
	}
	if hidden := x.s.HiddenMovies(ctx); len(hidden) != 0 {
		t.Fatalf("an accepted feed release left the movie hidden: %v", hidden)
	}
	if v.judged == 0 {
		t.Fatal("the first pass judged nothing")
	}

	// A restart on the same database answers from the durable verdicts.
	v.judged = 0
	restarted, err := New(x.s.d)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if hidden := restarted.HiddenMovies(ctx); len(hidden) != 0 {
			t.Fatalf("hidden after restart: %v", hidden)
		}
		if got := restarted.FilterDiscovery(ctx, []core.Title{movie}); len(got) != 1 {
			t.Fatalf("hub lost the movie: %v", got)
		}
		restarted.clearVisibility()
		x.now = x.now.Add(time.Minute)
	}
	if v.judged != 0 {
		t.Fatalf("section loads judged %d feed releases with verdicts stored", v.judged)
	}

	// The owner blocks the group: every verdict is judged again, exactly.
	x.c.Resolver.BlockedGroups = []string{"GRP"}
	r.UpdateConfig(x.c)
	restarted.clearVisibility()
	if hidden := restarted.HiddenMovies(ctx); len(hidden) != 1 || hidden[0] != movie.Key {
		t.Fatalf("a blocked group still unlocked the movie: %v", hidden)
	}
	if v.judged == 0 {
		t.Fatal("a policy change reused verdicts judged under the old policy")
	}
}
