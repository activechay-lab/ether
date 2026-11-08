package config

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// The shipped 4K/1080p/720p profiles as v0.3.5 and earlier stored them.
var legacyShippedProfiles = map[string]string{
	"4K":    `{"name":"4K","maxHeight":2160,"minHeight":720,"allowHdr":true,"allowDolbyVision":true,"preferredSources":["remux","bluray","webdl","webrip"],"preferredCodecs":["hevc","av1","h264"],"preferredAudio":["truehd","eac3","dtshd","ac3","aac"],"minSizeMbPerMin":15,"maxSizeMbPerMin":1000,"ranking":["language","connection","resolution","releaseType","hdr","customScore","verified","videoCodec","audioFormat","size"]}`,
	"1080p": `{"name":"1080p","maxHeight":1080,"minHeight":720,"preferredSources":["webdl","bluray","webrip","hdtv"],"preferredCodecs":["h264","hevc"],"preferredAudio":["eac3","ac3","aac"],"minSizeMbPerMin":5,"maxSizeMbPerMin":150,"ranking":["language","connection","resolution","releaseType","hdr","customScore","verified","videoCodec","audioFormat","size"]}`,
	"720p":  `{"name":"720p","maxHeight":720,"minHeight":480,"preferredSources":["webdl","webrip","bluray","hdtv"],"preferredCodecs":["h264"],"preferredAudio":["aac","ac3","eac3"],"minSizeMbPerMin":2,"maxSizeMbPerMin":60}`,
}

// Ether's own profiles are what converting the profiles it used to ship
// produces, so a fresh install and an upgraded one rank alike (the resolver's
// ranking corpus proves the converted picks against the old ones).
func TestDefaultProfilesAreTheConvertedShippedProfiles(t *testing.T) {
	for _, p := range DefaultQuality().Profiles {
		var legacy legacyProfile
		if err := json.Unmarshal([]byte(legacyShippedProfiles[p.Name]), &legacy); err != nil {
			t.Fatal(err)
		}
		conv := convertProfile(legacy, legacyResolver{})
		if !maps.Equal(conv.scores, p.CustomFormatScores) {
			t.Errorf("%s scores:\n default   %v\n converted %v", p.Name, p.CustomFormatScores, conv.scores)
		}
		if !reflect.DeepEqual(conv.order, p.QualityOrder) {
			t.Errorf("%s quality order:\n default   %v\n converted %v", p.Name, p.QualityOrder, conv.order)
		}
		if len(conv.notes) > 0 {
			t.Errorf("%s: shipped ladder converted with notes %v", p.Name, conv.notes)
		}
	}
}

// A stored config is converted once on start: the ladder fields go, the
// global scores move onto the profile, and the result is saved.
func TestOpenConvertsStoredRanking(t *testing.T) {
	dir := t.TempDir()
	doc := `{"resolver":{"customFormats":[{"id":"lq","name":"LQ","specifications":[{"name":"g","implementation":"ReleaseGroupSpecification","fields":{"value":"^YIFY$"}}]}],"customFormatScores":{"lq":-500},"minCustomFormatScore":-1000},
"quality":{"default":"4K","profiles":[` + legacyShippedProfiles["4K"][:len(legacyShippedProfiles["4K"])-1] + `,"mediaType":"movie","releaseRules":[{"id":"r1","name":"FLUX","action":"prefer","conditions":[{"field":"group","operator":"equals","value":"FLUX"}]}],"rankingConfig":{"plugin:x.y/boost":{"lang":"fr"}},"ranking":["language","connection","resolution","releaseType","hdr","customScore","verified","videoCodec","audioFormat","size","plugin:x.y/boost"]}]}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := st.Get().Quality.Profiles[0]
	if p.CustomFormatScores["lq"] != -500 || p.MinCustomFormatScore == nil || *p.MinCustomFormatScore != -1000 {
		t.Fatalf("global scores not moved onto the profile: %v min %v", p.CustomFormatScores, p.MinCustomFormatScore)
	}
	if p.CustomFormatScores[core.RuleFormatPrefix+"r1"] <= p.CustomFormatScores[core.FormatViewerLanguage] {
		t.Fatalf("prefer rule should outweigh every built-in level: %v", p.CustomFormatScores)
	}
	if string(p.PluginScores["plugin:x.y/boost"]) != `{"lang":"fr"}` {
		t.Fatalf("plugin step config = %s", p.PluginScores)
	}
	// The Prefer rule ranked first, above resolution: every quality is one
	// group and resolution a format below the rule, so nothing reorders.
	if len(p.QualityOrder) != 1 || p.QualityOrder[0].Name != ConvertedOrderGroup || !p.ConvertedRanking {
		t.Fatalf("quality order = %+v", p.QualityOrder)
	}
	rule, uhd, hd := p.CustomFormatScores[core.RuleFormatPrefix+"r1"], p.CustomFormatScores[core.ResolutionFormat(2160)], p.CustomFormatScores[core.ResolutionFormat(1080)]
	if !(rule > uhd && uhd > hd && hd > p.CustomFormatScores[core.FormatViewerLanguage]) {
		t.Fatalf("rule %d, 2160p %d, 1080p %d: want rule > 2160p > 1080p > viewer's language", rule, uhd, hd)
	}
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	if changed, _ := upgradeRankingDocument(saved); changed {
		t.Fatal("saved config still holds ladder fields")
	}
}
