package resolver

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// A plugin's points join the custom format score: they decide between
// releases of one quality and never lift a release over a better quality.
func TestSortRankingCandidatesPluginPoints(t *testing.T) {
	step := "plugin:example/dub"
	profile := core.QualityProfile{QualityOrder: []core.QualityItem{{Qualities: []string{"webdl-2160"}}, {Qualities: []string{"webdl-1080"}}}}
	web := func(id string, h, cf int) RankingCandidate {
		return RankingCandidate{Release: core.Release{ID: id, Info: core.ReleaseInfo{Height: h, Source: core.SourceWEBDL}}, LanguageOK: true, CustomFormatScore: cf, PluginKeys: map[string]int64{step: int64(cf)}}
	}
	c := []RankingCandidate{web("plain", 2160, 0), web("dub", 1080, 500)}
	SortRankingCandidates(c, profile, nil)
	if c[0].Release.ID != "plain" {
		t.Fatalf("plugin points lifted a lower quality: %v", c[0].Release.ID)
	}
	c = []RankingCandidate{web("plain", 1080, 0), web("dub", 1080, 500)}
	SortRankingCandidates(c, profile, nil)
	if c[0].Release.ID != "dub" {
		t.Fatalf("plugin points did not decide within a quality: %v", c[0].Release.ID)
	}
}
