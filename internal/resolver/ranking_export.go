package resolver

import (
	"sort"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releasepolicy"
)

// RankingCandidate holds the evidence already computed for a candidate. Keys
// are indexed by the complete plugin step ID.
type RankingCandidate struct {
	Index                                         int
	Release                                       core.Release
	Policy                                        releasepolicy.Result
	Verified, LanguageOK, LanguageWanted, Starved bool
	Bandwidth                                     int
	// CustomFormatScore is the release's custom format score, plugin
	// points included.
	CustomFormatScore int
	PluginKeys        map[string]int64
}

// SortRankingCandidates applies the order playback uses (ranking.go). The
// sort is stable, so equal candidates retain their input order.
func SortRankingCandidates(candidates []RankingCandidate, profile core.QualityProfile, title *core.Title) {
	rc := rankContext{profile: profile, title: title, now: time.Now(), preferRevision: profile.CustomFormatScores[core.FormatRepackProper] == 0}
	convert := func(c RankingCandidate) candidate {
		return candidate{rel: c.Release, policy: c.Policy, verified: c.Verified, langOK: c.LanguageOK, langWanted: c.LanguageWanted, starved: c.Starved, bandwidth: c.Bandwidth, pluginKeys: c.PluginKeys, cf: c.CustomFormatScore, keys: keysFor(c.Release, c.Starved, rc)}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return compareCandidates(convert(candidates[i]), convert(candidates[j]))
	})
}
