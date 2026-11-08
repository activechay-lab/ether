package resolver

import (
	"maps"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releasepolicy"
)

// withDefaults gives each format the score scores names as its default
// (guide) score, for the test helpers below.
func withDefaults(defs []core.CustomFormat, scores map[string]int) []core.CustomFormat {
	out := make([]core.CustomFormat, len(defs))
	for i, d := range defs {
		id := d.ID
		if id == "" {
			id = d.Name
		}
		if v, ok := scores[id]; ok {
			d.Scores = maps.Clone(d.Scores)
			if d.Scores == nil {
				d.Scores = map[string]int{}
			}
			d.Scores["default"] = v
		}
		out[i] = d
	}
	return out
}

// defaultScores is a profile score map giving every format its default
// (guide) score.
func (s *FormatScorer) defaultScores() map[string]int {
	out := map[string]int{}
	for _, f := range s.formats {
		if v, ok := f.def.Scores["default"]; ok {
			out[f.def.ID] = v
		}
	}
	return out
}

// Score scores r as a profile listing every format at its default score.
func (s *FormatScorer) Score(r core.Release) (int, []FormatMatch, error) {
	return s.ScoreWithScores(r, nil)
}

// ScoreWithScores is Score with some formats' scores overridden.
func (s *FormatScorer) ScoreWithScores(r core.Release, overrides map[string]int) (int, []FormatMatch, error) {
	return s.ScoreForTitle(r, overrides, "")
}

// ScoreForTitle is ScoreWithScores for a title whose original language is
// original; the release's languages are taken as they are.
func (s *FormatScorer) ScoreForTitle(r core.Release, overrides map[string]int, original string) (int, []FormatMatch, error) {
	scores := s.defaultScores()
	maps.Copy(scores, overrides)
	return s.scoreFormats(r, scores, original)
}

// languageScore sums the language formats a release matches for prefs at
// the scores converted profiles give them.
func languageScore(info core.ReleaseInfo, prefs []string) float64 {
	scores := map[string]int{core.FormatPreferredLanguage: 5, core.FormatPreferredLanguageRegion: 2, core.FormatOtherLanguage: -25, core.FormatPreferredSubtitles: 2}
	total := 0
	for _, id := range languageFormats(info, prefs) {
		total += scores[id]
	}
	return float64(total)
}

// languageScore is the custom format score a release earns from a
// profile's Languages alone, with the built-in language formats at the
// scores converted profiles carry.
func (s *Service) languageScore(r core.Release, p core.QualityProfile, t *core.Title) float64 {
	p.CustomFormatScores = maps.Clone(p.CustomFormatScores)
	if p.CustomFormatScores == nil {
		p.CustomFormatScores = map[string]int{}
	}
	for id, v := range map[string]int{core.FormatPreferredLanguage: 5, core.FormatPreferredLanguageRegion: 2, core.FormatOtherLanguage: -25, core.FormatPreferredSubtitles: 2} {
		if _, ok := p.CustomFormatScores[id]; !ok {
			p.CustomFormatScores[id] = v
		}
	}
	sc, err := s.scoreRelease(r, p, core.ClientCaps{}, t, releasepolicy.Result{}, formatFacts{})
	if err != nil {
		panic(err)
	}
	return float64(sc.total)
}
