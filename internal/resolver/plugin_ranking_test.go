package resolver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

type dubHooks struct{ batches int }

func (d *dubHooks) Close()                                                     {}
func (d *dubHooks) NewResolve(context.Context, core.ClientCaps) RankingSession { return d }
func (d *dubHooks) Rank(_ string, _ json.RawMessage, rels []core.Release, _ []bool) []int64 {
	d.batches++
	keys := make([]int64, len(rels))
	for i, r := range rels {
		if strings.Contains(r.Name, "DUB") {
			keys[i] = 1
		}
	}
	return keys
}
func (d *dubHooks) Match(r core.Release, _ core.ReleaseCondition, _ bool) (bool, bool) {
	return strings.Contains(r.Name, "DUB"), true
}
func TestPluginRankingOnRealResolve(t *testing.T) {
	plain := signalRelease("plain", "GROUP", "english")
	dub := signalRelease("dub", "GROUP", "english")
	dub.Name = strings.Replace(dub.Name, "1080p", "DUB.1080p", 1)
	cfg := testCfg()
	for i := range cfg.Quality.Profiles {
		cfg.Quality.Profiles[i].PluginScores = map[string]json.RawMessage{"plugin:test.dub/dub": json.RawMessage(`{}`)}
	}
	s := New(&fakeIndexer{releases: []core.Release{plain, dub}}, fakeHealth{}, nil, cfg, Options{})
	defer s.Close()
	hooks := &dubHooks{}
	s.SetRankingHooks(hooks)
	got, err := s.Resolve(context.Background(), core.ResolveRequest{Title: matrixTitle(), Client: webCaps()})
	if err != nil {
		t.Fatal(err)
	}
	if got.Release.ID != "dub" {
		t.Fatalf("winner=%s", got.Release.ID)
	}
	if hooks.batches != 1 {
		t.Fatalf("rank batches=%d", hooks.batches)
	}
}
func TestPluginConditionOnResolve(t *testing.T) {
	plain := signalRelease("plain", "GROUP", "english")
	dub := signalRelease("dub", "GROUP", "english")
	dub.Name += ".DUB"
	rules := []core.ReleaseRule{{ID: "dub", Name: "Dub", Action: "require", Conditions: []core.ReleaseCondition{{Field: "plugin:test.dub/dub", Operator: "equals", Value: "yes"}}}}
	s := serviceWithRules(t, &fakeIndexer{releases: []core.Release{plain, dub}}, fakeHealth{}, rules)
	s.SetRankingHooks(&dubHooks{})
	got, err := s.Resolve(context.Background(), core.ResolveRequest{Title: matrixTitle(), Client: webCaps()})
	if err != nil {
		t.Fatal(err)
	}
	if got.Release.ID != "dub" {
		t.Fatalf("winner=%s", got.Release.ID)
	}
}
