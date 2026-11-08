package transcode

import (
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/langtag"
)

// matchReplacementStream finds the stream in info that best matches prev across
// release failovers and version switches:
//   - Protects main dialogue audio from accidentally swapping onto commentary
//     or audio-description tracks that share the same language tag.
//   - Preserves intentionally selected commentary or audio-description roles
//     when the replacement release carries a compatible track in that role,
//     falling back to same-language main dialogue only when the role is absent.
//   - Evaluates extended BCP-47 language rank (including stream title language
//     inference for "und"/"mul"/blank tags and regional/script refinements).
//   - Breaks audio ties by codec compatibility, channel-count proximity,
//     channel layout, and default disposition.
//   - Never invents subtitles the viewer did not pick, and never replaces a
//     forced subtitle track with a full non-forced track.
func matchReplacementStream(info core.MediaInfo, prev *core.StreamInfo, typ string) *core.StreamInfo {
	var first, def, firstMain, defMain *core.StreamInfo
	for i := range info.Streams {
		s := &info.Streams[i]
		if s.Type != typ || s.External {
			continue
		}
		if first == nil {
			first = s
		}
		if s.Default && def == nil {
			def = s
		}
		if typ == "audio" && streamRole(s) == langtag.RoleMain {
			if firstMain == nil {
				firstMain = s
			}
			if s.Default && defMain == nil {
				defMain = s
			}
		}
	}

	if prev == nil {
		if typ == "subtitle" {
			return nil
		}
		if defMain != nil {
			return defMain
		}
		if def != nil {
			return def
		}
		if firstMain != nil {
			return firstMain
		}
		return first
	}

	prevRole := streamRole(prev)
	var best *core.StreamInfo
	bestScore := -1

	for i := range info.Streams {
		s := &info.Streams[i]
		if s.Type != typ || s.External {
			continue
		}
		langRank := streamPairLanguageRank(prev, s)
		if langRank <= 0 {
			continue
		}

		if typ == "subtitle" {
			// Never replace a forced subtitle track with a full dialogue subtitle track.
			if prev.Forced && !s.Forced {
				continue
			}
			score := langRank * 1000
			if s.Forced == prev.Forced {
				score += 500
			}
			if s.Codec == prev.Codec {
				score += 200
			}
			if isSDHTitle(s.Title) == isSDHTitle(prev.Title) {
				score += 50
			}
			if s.Default {
				score += 5
			}
			if score > bestScore {
				best, bestScore = s, score
			}
			continue
		}

		candRole := streamRole(s)
		roleBonus := 0
		if prevRole == langtag.RoleMain {
			if candRole != langtag.RoleMain {
				// Never swap main dialogue onto commentary or audio description
				// as a language match.
				continue
			}
			roleBonus = 10000
		} else {
			switch candRole {
			case prevRole:
				roleBonus = 10000
			case langtag.RoleMain:
				// Acceptable fallback if the replacement release has no track
				// with the same special role in this language.
				roleBonus = 0
			default:
				// Do not swap commentary onto audio description or vice versa.
				continue
			}
		}

		score := roleBonus + langRank*1000
		if s.Codec == prev.Codec && s.Forced == prev.Forced {
			score += 250
		}
		if prev.Channels > 0 && s.Channels > 0 {
			diff := s.Channels - prev.Channels
			if diff < 0 {
				diff = -diff
			}
			score += max(0, 10-diff) * 20
		}
		if prev.ChannelLayout != "" && s.ChannelLayout == prev.ChannelLayout {
			score += 25
		}
		if s.Default {
			score += 5
		}
		if score > bestScore {
			best, bestScore = s, score
		}
	}

	if best != nil {
		return best
	}
	if typ == "subtitle" {
		return nil // never invent subtitles the user did not pick
	}
	if defMain != nil {
		return defMain
	}
	if firstMain != nil {
		return firstMain
	}
	if def != nil {
		return def
	}
	return first
}

func streamRole(s *core.StreamInfo) string {
	if s == nil {
		return langtag.RoleMain
	}
	return langtag.TrackRole(s.Title, s.Commentary, s.VisualImpaired)
}

func streamPairLanguageRank(prev, cand *core.StreamInfo) int {
	if prev == nil || cand == nil {
		return 0
	}
	prevLangs := langtag.EffectiveLanguages(prev.Language, prev.Title)
	candLangs := langtag.EffectiveLanguages(cand.Language, cand.Title)

	best := langtag.BestStreamMatchRank(prev.Language, cand.Language, cand.Title)
	for _, pl := range prevLangs {
		if r := langtag.BestStreamMatchRank(pl, cand.Language, cand.Title); r > best {
			best = r
		}
	}
	if best > 0 {
		return best
	}
	// Preserve legacy matching for untagged streams (both empty/undetermined
	// with no language in the title).
	if len(prevLangs) == 0 && len(candLangs) == 0 && strings.EqualFold(strings.TrimSpace(prev.Language), strings.TrimSpace(cand.Language)) {
		return langtag.RankExact
	}
	return 0
}

func isSDHTitle(title string) bool {
	v := strings.ToLower(title)
	return strings.Contains(v, "sdh") || strings.Contains(v, "hearing impaired") || strings.Contains(v, "[cc]") || v == "cc"
}
