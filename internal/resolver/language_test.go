package resolver

import (
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
	"time"
)

func TestLanguageRankingRequiresExplicitPreference(t *testing.T) {
	s := New(&fakeIndexer{}, nil, nil, testCfg(), Options{Now: func() time.Time { return time.Unix(1800000000, 0) }})
	defer s.Close()
	r := withParsed(rel("language", "The.Signal.1999.1080p.WEB-DL.H264-GRP", 4000))
	score := func(lang string, prefs ...string) float64 {
		r.Info.Languages = []string{lang}
		return s.languageScore(r, core.QualityProfile{PreferredLanguages: prefs}, matrixTitle())
	}
	for _, lang := range []string{"french", "german", "japanese", "multi"} {
		if score(lang) != score("english") {
			t.Errorf("default language penalty for %s", lang)
		}
	}
	if score("french", "french") <= score("english", "french") {
		t.Fatal("explicit French preference did not outrank English")
	}
	if score("english", "english") <= score("french", "english") {
		t.Fatal("explicit English preference ignored")
	}
}

// A profile Language is Radarr's hard filter: a specific language rejects
// releases whose audio isn't in it, Original keeps today's behaviour and
// Any accepts everything. The anime case: on a Japanese title an English
// profile rejects the Japanese release however good its quality is.
func TestProfileLanguageGate(t *testing.T) {
	anime := &core.Title{Language: "ja"}
	english := core.QualityProfile{Language: "en"}
	info := func(langs ...string) core.ReleaseInfo { return core.ReleaseInfo{Languages: langs} }

	for _, tt := range []struct {
		name    string
		profile core.QualityProfile
		title   *core.Title
		info    core.ReleaseInfo
		reject  bool
	}{
		{"4K Japanese on an English profile", english, anime, info("japanese"), true},
		{"1080p English dub", english, anime, info("english"), false},
		{"multi audio", english, anime, info("multi"), false},
		{"dual audio", english, anime, info("dual"), false},
		{"untagged counts as the original", english, anime, info(), true},
		{"untagged, original unknown", english, &core.Title{}, info(), false},
		{"untagged English original", english, &core.Title{Language: "en"}, info(), false},
		{"no language set keeps today", core.QualityProfile{}, anime, info("japanese"), false},
		{"Original keeps today", core.QualityProfile{Language: "original"}, anime, info("french"), false},
		{"Any accepts everything", core.QualityProfile{Language: "any"}, anime, info("french"), false},
		{"unrecognised language keeps today", core.QualityProfile{Language: "klingon"}, anime, info("japanese"), false},
		{"regional variant of the profile language", core.QualityProfile{Language: "es"}, anime, info("spanish-latino"), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reason := profileLanguageRejection(tt.info, tt.profile, tt.title)
			if (reason != "") != tt.reject {
				t.Fatalf("reason=%q reject=%v", reason, tt.reject)
			}
		})
	}

	// A dub into the profile's language is what the language asks for, so
	// the dub-mismatch check must not reject it; an explicit pick (a pin)
	// bypasses the gate.
	r := core.Release{Name: "Primetime (2026) 1080p WEB-DL [Eng Dub]", Info: core.ReleaseInfo{Source: core.SourceWEBDL}}
	if reason := sourceSuitability(r, anime, english, false); reason != "" {
		t.Fatalf("English dub on an English profile: %q", reason)
	}
	r = core.Release{Name: "Primetime (2026) 2160p WEB-DL [Jpn Dub]", Info: core.ReleaseInfo{Source: core.SourceWEBDL}}
	if reason := sourceSuitability(r, anime, english, true); reason != "" {
		t.Fatalf("explicit pick bypasses the gate: %q", reason)
	}

	// Accepted languages follow the mode: Original adds the title's own
	// language, a specific language accepts only itself, Any all.
	if got := acceptedAudio(core.QualityProfile{}, nil, "en", anime); len(got) != 2 || got[1] != "ja" {
		t.Fatalf("original accepted=%v", got)
	}
	if got := acceptedAudio(english, nil, "en", anime); len(got) != 1 || got[0] != "english" {
		t.Fatalf("specific accepted=%v", got)
	}
	if got := acceptedAudio(core.QualityProfile{Language: "any"}, nil, "en", anime); got != nil {
		t.Fatalf("any accepted=%v", got)
	}
	if got := wantedAudio(english, nil, "fr"); len(got) != 1 || got[0] != "english" {
		t.Fatalf("specific wanted=%v", got)
	}
}
