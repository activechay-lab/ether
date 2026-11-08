package resolver

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
)

// The registry-aware preview path accepts registered plugin fields and
// rejects unregistered ones (WS1a item 4).
func TestServiceRegistryValidation(t *testing.T) {
	svc := New(nil, nil, nil, testCfg(), Options{})
	pluginStep := "plugin:example.dub/lang"
	reg := extend.New()
	svc.SetExtendRegistry(reg)

	step := extend.Descriptor{
		Kind: extend.KindRankingStep, ID: pluginStep, Owner: "example.dub",
		Label: extend.PluginText("example.dub", "step.lang"),
	}.WithMeta(extend.RankingStepMeta{Editor: extend.RankingEditorNone, CanMove: true, DefaultPosition: 1})
	field := extend.Descriptor{
		Kind: extend.KindRuleField, ID: "plugin:example.dub/dub_lang", Owner: "example.dub",
		Label: extend.PluginText("example.dub", "field.dub_lang"),
	}.WithMeta(extend.RuleFieldMeta{ValueClass: extend.RuleValueChoice, Operators: []string{"equals"}, DefaultOperator: "equals", SingleValue: true})
	// Manifest-shaped descriptors produced by plugins.DescriptorsFor
	// (editor as string/object/null instead of valueClass).
	manifestField := extend.Descriptor{
		Kind: extend.KindRuleField, ID: "plugin:example.dub/dubbed", Owner: "example.dub",
		Label: extend.Text{Resolved: "Dubbed"},
	}.WithMeta(map[string]any{"operators": nil, "editor": nil})
	manifestRangeField := extend.Descriptor{
		Kind: extend.KindRuleField, ID: "plugin:example.dub/score", Owner: "example.dub",
		Label: extend.Text{Resolved: "Dub score"},
	}.WithMeta(map[string]any{"operators": []string{"range"}, "editor": "range"})
	if err := reg.Register(step, field, manifestField, manifestRangeField); err != nil {
		t.Fatal(err)
	}

	dubRules := []core.ReleaseRule{
		{ID: "dub", Name: "Dub", Action: "prefer", Conditions: []core.ReleaseCondition{{Field: field.ID, Operator: "equals", Value: "fr"}}},
		{ID: "dubbed", Name: "Dubbed", Action: "prefer", Conditions: []core.ReleaseCondition{{Field: manifestField.ID, Operator: "equals", Value: "yes"}}},
		{ID: "score", Name: "Score", Action: "prefer", Conditions: []core.ReleaseCondition{{Field: manifestRangeField.ID, Operator: "range", Min: 1, Max: 10}}},
	}
	if _, err := svc.compileRules(dubRules); err != nil {
		t.Fatalf("registered plugin field should compile: %v", err)
	}
	badRules := []core.ReleaseRule{
		{ID: "dub", Name: "Dub", Action: "prefer", Conditions: []core.ReleaseCondition{{Field: field.ID, Operator: "contains", Value: "fr"}}},
	}
	if _, err := svc.compileRules(badRules); err == nil {
		t.Fatal("operator outside the registered field's declared set must fail")
	}
	// Single-value plugin fields take part in conflict detection.
	conflict := []core.ReleaseRule{
		{ID: "a", Name: "French dub", Action: "require", Conditions: []core.ReleaseCondition{{Field: field.ID, Operator: "equals", Value: "fr"}}},
		{ID: "b", Name: "German dub", Action: "require", Conditions: []core.ReleaseCondition{{Field: field.ID, Operator: "equals", Value: "de"}}},
	}
	if _, err := svc.compileRules(conflict); err != nil {
		t.Fatal(err)
	}
	specs := svc.fieldSpecs()
	if specs == nil || !specs[field.ID].External || !specs[field.ID].SingleValue {
		t.Fatalf("registry field spec not merged: %+v", specs[field.ID])
	}
	if specs[manifestRangeField.ID].ValueClass != extend.RuleValueNumber {
		t.Fatalf("manifest range field valueClass = %q, want %q", specs[manifestRangeField.ID].ValueClass, extend.RuleValueNumber)
	}
	if _, ok := specs["group"]; !ok {
		t.Fatal("built-in fields must survive the registry merge")
	}
}
