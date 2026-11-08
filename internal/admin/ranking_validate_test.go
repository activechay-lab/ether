package admin

import (
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestValidateConfigQualityProfileRanking(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	if errs := ValidateConfig(cfg); len(errs) != 0 {
		t.Fatalf("Defaults failed ValidateConfig: %v", errs)
	}
	p := &cfg.Quality.Profiles[0]
	p.ReleaseRules = []core.ReleaseRule{{ID: "r1", Name: "Prefer FLUX", Action: "prefer",
		Conditions: []core.ReleaseCondition{{Field: "group", Operator: "equals", Value: "FLUX"}}}}
	p.CustomFormatScores[core.RuleFormatPrefix+"r1"] = 50000
	if errs := ValidateConfig(cfg); len(errs) != 0 {
		t.Fatalf("a scored Prefer rule failed ValidateConfig: %v", errs)
	}
	has := func(errs []FieldError, field, msg string) bool {
		for _, e := range errs {
			if e.Field == field && strings.Contains(e.Message, msg) {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct {
		name  string
		edit  func(*core.QualityProfile)
		field string
		msg   string
	}{

		{"unknown quality", func(p *core.QualityProfile) { p.QualityOrder = []core.QualityItem{{Qualities: []string{"vhs-480"}}} }, "quality.profiles[0].qualityOrder", "not a quality"},
		{"quality listed twice", func(p *core.QualityProfile) {
			p.QualityOrder = []core.QualityItem{{Qualities: []string{"webdl-1080"}}, {Qualities: []string{"webdl-1080"}}}
		}, "quality.profiles[0].qualityOrder", "twice"},
		{"unknown built-in format", func(p *core.QualityProfile) { p.CustomFormatScores["ether:nope"] = 5 }, "quality.profiles[0].customFormatScores", "not one of Ether's formats"},
		{"rule score without the rule", func(p *core.QualityProfile) { p.CustomFormatScores["rule:gone"] = 5 }, "quality.profiles[0].customFormatScores", "Prefer rule"},
	} {
		c := cfg
		c.Quality.Profiles = append([]core.QualityProfile(nil), cfg.Quality.Profiles...)
		q := c.Quality.Profiles[0]
		q.CustomFormatScores = map[string]int{}
		for k, v := range p.CustomFormatScores {
			q.CustomFormatScores[k] = v
		}
		tc.edit(&q)
		c.Quality.Profiles[0] = q
		if errs := ValidateConfig(c); !has(errs, tc.field, tc.msg) {
			t.Errorf("%s: want %s error %q, got %v", tc.name, tc.field, tc.msg, errs)
		}
	}
}
