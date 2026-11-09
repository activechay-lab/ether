package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/resolver"
)

func TestRankingConfigValidatedOnSave(t *testing.T) {
	reg := extend.New()
	d := extend.Descriptor{Kind: extend.KindRankingStep, ID: "plugin:test.dub/dub", Owner: "test.dub", Label: extend.Text{Resolved: "Dub"}, Meta: json.RawMessage(`{"editor":{"kind":"toggle"}}`)}
	if err := reg.Register(d); err != nil {
		t.Fatal(err)
	}
	res := &resolver.Service{}
	res.SetExtendRegistry(reg)
	a := &App{resolver: res}
	cfg := config.Defaults(t.TempDir())
	cfg.Indexers = nil
	cfg.Quality.Profiles[0].PluginScores = map[string]json.RawMessage{d.ID: json.RawMessage(`{"value":"bad"}`)}
	errs := a.validateExtra(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Field, "pluginScores.plugin:test.dub/dub") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing field error: %v", errs)
	}
	cfg.Quality.Profiles[0].PluginScores[d.ID] = json.RawMessage(`{"value":true}`)
	for _, e := range a.validateExtra(cfg) {
		if strings.Contains(e.Field, "pluginScores") {
			t.Fatalf("valid configuration rejected: %v", e)
		}
	}
}
