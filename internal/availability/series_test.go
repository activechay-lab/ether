package availability

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/resolver"
	"testing"
	"time"
)

func (c *fakeCatalog) CachedDiscoveryEpisodes(_ context.Context, keys []core.RatingKey) (map[core.RatingKey][]core.Title, error) {
	out := map[core.RatingKey][]core.Title{}
	for _, k := range keys {
		for _, t := range c.titles {
			if t.Type == core.TypeEpisode && t.ShowKey == k && t.Season > 0 {
				out[k] = append(out[k], t)
			}
		}
	}
	return out, nil
}
func (c *fakeCatalog) RecentShows(_ context.Context, since time.Time) ([]core.Title, error) {
	var out []core.Title
	for _, t := range c.titles {
		if t.Type == core.TypeShow && t.Released.After(since) {
			out = append(out, t)
		}
	}
	return out, nil
}

func TestPremiereSeriesRequiresVisibleEpisode(t *testing.T) {
	x := setup(t)
	r := resolver.New(nil, nil, nil, x.c, resolver.Options{})
	defer r.Close()
	x.s.d.Validator = suitabilityValidator{x.v, r}
	show := core.Title{Key: core.ShowKey(7), Type: core.TypeShow, Title: "Series", Language: "en", Released: x.now.Add(-time.Hour)}
	x.cat.titles[show.Key] = show
	check := func(target core.Title, want bool) {
		t.Helper()
		x.s.clearVisibility()
		got := x.s.FilterDiscovery(t.Context(), []core.Title{target})
		if (len(got) == 1) != want {
			t.Fatalf("show visible=%v want %v", len(got) == 1, want)
		}
		hidden := x.s.HiddenShows(t.Context())
		isHidden := false
		for _, k := range hidden {
			if k == target.Key {
				isHidden = true
			}
		}
		if isHidden == want {
			t.Fatalf("pagination exclusions=%v, visible=%v", hidden, want)
		}
	}
	check(show, false)
	put := func(id, name string) {
		t.Helper()
		if err := x.s.retainFeed(t.Context(), x.f.sources[0], []core.Release{{ID: id, Name: name, Indexer: "Fake"}}); err != nil {
			t.Fatal(err)
		}
	}
	put("wrong", "Series.S01E02.1080p.WEB-DL")
	put("cam", "Series.S01E01.1080p.CAM")
	put("pack", "Series.S01.1080p.WEB-DL") // Unsupported Usenet packs are not playable evidence.
	check(show, false)
	put("good", "Series.S01E01.1080p.WEB-DL")
	check(show, true) // Feed arrived before any episode metadata.
	x.s.Invalidate(t.Context(), "good")
	check(show, true) // Sticky gate stays open once opened.
	x.f.sources[0].Generation = "changed"
	check(show, false)
	x.f.sources[0].Generation = "one"
	check(show, true)
	ep := core.Title{Key: core.EpisodeKey(7, 1, 1), ShowKey: show.Key, Type: core.TypeEpisode, Season: 1, Episode: 1, Released: x.now.Add(time.Hour)}
	x.cat.titles[ep.Key] = ep
	check(show, false) // Actual future date beats fallback.
	ep.Released = time.Time{}
	x.cat.titles[ep.Key] = ep
	check(show, false)
	ep.Released = x.now.Add(-time.Hour)
	x.cat.titles[ep.Key] = ep
	check(show, true)
	ep.Adult = true
	x.cat.titles[ep.Key] = ep
	check(show, false)
	ep.Adult = false
	x.cat.titles[ep.Key] = ep
	check(show, true)

	// A second premiere series with no evidence unlocks after the 24h assumption.
	show2 := core.Title{Key: core.ShowKey(8), Type: core.TypeShow, Title: "Other", Language: "en", Released: x.now.Add(-2 * time.Hour)}
	ep2 := core.Title{Key: core.EpisodeKey(8, 1, 1), ShowKey: show2.Key, Type: core.TypeEpisode, Season: 1, Episode: 1, Released: show2.Released}
	x.cat.titles[show2.Key] = show2
	x.cat.titles[ep2.Key] = ep2
	check(show2, false)
	x.now = x.now.Add(23 * time.Hour)
	check(show2, true)
	if x.f.calls != 0 || x.v.calls != 0 {
		t.Fatal("browsing spent provider quota")
	}
}

func TestPremiereSeriesAcceptsAnotherReleasedEpisodeButNotFutureShow(t *testing.T) {
	x := setup(t)
	show := core.Title{Key: core.ShowKey(7), Type: core.TypeShow, Title: "Series", Released: x.now.Add(-time.Hour)}
	ep := core.Title{Key: core.EpisodeKey(7, 1, 2), ShowKey: show.Key, Type: core.TypeEpisode, Season: 1, Episode: 2, Released: show.Released}
	x.cat.titles[ep.Key] = ep
	if err := x.s.remember(t.Context(), x.f.sources[0], ep, x.release("ep"), x.s.policy()); err != nil {
		t.Fatal(err)
	}
	if len(x.s.FilterDiscovery(t.Context(), []core.Title{show})) != 1 {
		t.Fatal("visible child did not unlock series")
	}
	show.Released = x.now.Add(time.Hour)
	if len(x.s.FilterDiscovery(t.Context(), []core.Title{show})) != 0 {
		t.Fatal("future show unlocked")
	}
}
