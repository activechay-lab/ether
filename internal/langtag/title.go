package langtag

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
)

// displayLanguageAliases maps lower-case display names and regional/script
// phrases to their canonical BCP-47 tags.
var displayLanguageAliases = map[string]string{
	"english":                 "en",
	"spanish":                 "es",
	"español":                 "es",
	"espanol":                 "es",
	"french":                  "fr",
	"français":                "fr",
	"francais":                "fr",
	"german":                  "de",
	"deutsch":                 "de",
	"italian":                 "it",
	"italiano":                "it",
	"portuguese":              "pt",
	"português":               "pt",
	"portugues":               "pt",
	"japanese":                "ja",
	"korean":                  "ko",
	"chinese":                 "zh",
	"mandarin":                "zh",
	"russian":                 "ru",
	"arabic":                  "ar",
	"dutch":                   "nl",
	"polish":                  "pl",
	"swedish":                 "sv",
	"norwegian":               "no",
	"danish":                  "da",
	"finnish":                 "fi",
	"greek":                   "el",
	"turkish":                 "tr",
	"hungarian":               "hu",
	"czech":                   "cs",
	"romanian":                "ro",
	"hebrew":                  "he",
	"hindi":                   "hi",
	"thai":                    "th",
	"vietnamese":              "vi",
	"indonesian":              "id",
	"ukrainian":               "uk",
	"bengali":                 "bn",
	"bangla":                  "bn",
	"bulgarian":               "bg",
	"croatian":                "hr",
	"persian":                 "fa",
	"farsi":                   "fa",
	"malay":                   "ms",
	"serbian":                 "sr",
	"slovak":                  "sk",
	"slovenian":               "sl",
	"tamil":                   "ta",
	"telugu":                  "te",
	"estonian":                "et",
	"latvian":                 "lv",
	"lithuanian":              "lt",
	"icelandic":               "is",
	"catalan":                 "ca",
	"welsh":                   "cy",
	"irish":                   "ga",
	"basque":                  "eu",
	"galician":                "gl",
	"afrikaans":               "af",
	"marathi":                 "mr",
	"gujarati":                "gu",
	"kannada":                 "kn",
	"malayalam":               "ml",
	"punjabi":                 "pa",
	"urdu":                    "ur",
	"nepali":                  "ne",
	"swahili":                 "sw",
	"kazakh":                  "kk",
	"uzbek":                   "uz",
	"mongolian":               "mn",
	"armenian":                "hy",
	"georgian":                "ka",
	"azerbaijani":             "az",
	"burmese":                 "my",
	"khmer":                   "km",
	"lao":                     "lo",
	"amharic":                 "am",
	"zulu":                    "zu",
	"latin":                   "la",
	"esperanto":               "eo",
	"brazilian portuguese":    "pt-br",
	"brazzillian portuguese":  "pt-br",
	"portuguese (brazil)":     "pt-br",
	"portuguese-brazil":       "pt-br",
	"português (brasil)":      "pt-br",
	"portugues (brasil)":      "pt-br",
	"european portuguese":     "pt-pt",
	"portuguese (portugal)":   "pt-pt",
	"portuguese-portugal":     "pt-pt",
	"português (portugal)":    "pt-pt",
	"portugues (portugal)":    "pt-pt",
	"spanish-latino":          "es-419",
	"spanish-spain":           "es-es",
	"latin american spanish":  "es-419",
	"spanish (latin america)": "es-419",
	"español (latinoamérica)": "es-419",
	"espanol (latinoamerica)": "es-419",
	"español latino":          "es-419",
	"espanol latino":          "es-419",
	"latino":                  "es-419",
	"castilian spanish":       "es-es",
	"spanish (spain)":         "es-es",
	"español (españa)":        "es-es",
	"espanol (espana)":        "es-es",
	"castilian":               "es-es",
	"castellano":              "es-es",
	"canadian french":         "fr-ca",
	"french (canada)":         "fr-ca",
	"français (canada)":       "fr-ca",
	"francais (canada)":       "fr-ca",
	"vfq":                     "fr-ca",
	"vff":                     "fr-fr",
	"french (france)":         "fr-fr",
	"traditional chinese":     "zh-hant",
	"chinese (traditional)":   "zh-hant",
	"simplified chinese":      "zh-hans",
	"chinese (simplified)":    "zh-hans",
	"cantonese":               "zh-hk",
	"serbian (latin)":         "sr-latn",
	"serbian (cyrillic)":      "sr-cyrl",
}

// titlePhrasePatterns are ordered regional/script phrases checked against a
// normalized title before token splitting so "Brazilian Portuguese" yields
// "pt-BR" instead of bare "pt" and "Spanish (Latin America)" consumes "Latin".
var titlePhrasePatterns = []struct {
	phrase string
	code   string
}{
	{"brazilian portuguese", "pt-BR"},
	{"brazillian portuguese", "pt-BR"},
	{"portuguese (brazil)", "pt-BR"},
	{"portuguese [brazil]", "pt-BR"},
	{"portugues (brasil)", "pt-BR"},
	{"português (brasil)", "pt-BR"},
	{"portugues brasil", "pt-BR"},
	{"português brasil", "pt-BR"},
	{"pt-br", "pt-BR"},
	{"pt_br", "pt-BR"},
	{"european portuguese", "pt-PT"},
	{"portuguese (portugal)", "pt-PT"},
	{"portugues (portugal)", "pt-PT"},
	{"português (portugal)", "pt-PT"},
	{"pt-pt", "pt-PT"},
	{"pt_pt", "pt-PT"},
	{"latin american spanish", "es-419"},
	{"spanish (latin america)", "es-419"},
	{"espanol (latinoamerica)", "es-419"},
	{"español (latinoamérica)", "es-419"},
	{"espanol latino", "es-419"},
	{"español latino", "es-419"},
	{"es-419", "es-419"},
	{"castilian spanish", "es-ES"},
	{"spanish (spain)", "es-ES"},
	{"español (españa)", "es-ES"},
	{"espanol (espana)", "es-ES"},
	{"castilian", "es-ES"},
	{"castellano", "es-ES"},
	{"es-es", "es-ES"},
	{"canadian french", "fr-CA"},
	{"french (canada)", "fr-CA"},
	{"français (canada)", "fr-CA"},
	{"francais (canada)", "fr-CA"},
	{"fr-ca", "fr-CA"},
	{"french (france)", "fr-FR"},
	{"fr-fr", "fr-FR"},
	{"chinese (traditional)", "zh-Hant"},
	{"traditional chinese", "zh-Hant"},
	{"zh-hant-tw", "zh-Hant-TW"},
	{"zh-hant", "zh-Hant"},
	{"zh-tw", "zh-TW"},
	{"chinese (simplified)", "zh-Hans"},
	{"simplified chinese", "zh-Hans"},
	{"zh-hans-cn", "zh-Hans-CN"},
	{"zh-hans", "zh-Hans"},
	{"zh-cn", "zh-CN"},
	{"zh-hk", "zh-HK"},
	{"serbian (latin)", "sr-Latn"},
	{"sr-latn", "sr-Latn"},
	{"serbian (cyrillic)", "sr-Cyrl"},
	{"sr-cyrl", "sr-Cyrl"},
}

// audioJunkTokens are codec, channel, and role words that appear beside a
// language in a stream title ("AC3 5.1 English+French") and must never be
// mistaken for language codes.
var audioJunkTokens = map[string]bool{
	"ac3": true, "eac3": true, "dts": true, "dtshd": true, "truehd": true, "atmos": true,
	"aac": true, "flac": true, "opus": true, "pcm": true, "mp3": true, "vorbis": true,
	"lpcm": true, "wav": true, "hevc": true, "h264": true, "av1": true, "pgs": true,
	"srt": true, "ass": true, "ssa": true, "vtt": true, "subrip": true, "dvdsub": true,
	"stereo": true, "mono": true, "surround": true, "commentary": true, "comments": true,
	"director": true, "directors": true, "cast": true, "description": true, "descriptive": true,
	"dub": true, "dubbed": true, "sub": true, "subs": true, "subtitle": true, "subtitles": true,
	"original": true, "multi": true, "dual": true, "audio": true, "track": true, "stream": true,
	"lang": true, "language": true, "mix": true, "mixed": true, "hd": true, "ma": true,
	"sdh": true, "cc": true, "ad": true, "hi": true, "forced": true, "full": true,
	"signs": true, "songs": true, "main": true, "primary": true, "default": true,
	"dolby": true, "digital": true, "plus": true, "lossless": true, "remux": true,
	"2.0": true, "5.1": true, "7.1": true, "5.1.2": true, "7.1.4": true,
	"2ch": true, "6ch": true, "8ch": true,
}

// validTwoLetterTokens are uppercase 2-letter ISO 639-1 codes accepted as
// standalone tokens in track titles when written in uppercase (e.g. "EN / FR").
var validTwoLetterTokens = map[string]bool{
	"en": true, "fr": true, "de": true, "es": true, "it": true, "pt": true,
	"nl": true, "ja": true, "ko": true, "zh": true, "ru": true, "ar": true,
	"sv": true, "no": true, "da": true, "fi": true, "pl": true, "tr": true,
	"cs": true, "hu": true, "uk": true, "el": true, "he": true, "th": true,
	"vi": true, "id": true, "ro": true, "bg": true, "hr": true, "sr": true,
	"sk": true, "sl": true, "et": true, "lv": true, "lt": true, "fa": true,
	"ms": true, "ta": true, "te": true, "ca": true,
}

func findPhraseBoundary(haystack, needle string) (int, int, bool) {
	idx := 0
	for {
		pos := strings.Index(haystack[idx:], needle)
		if pos < 0 {
			return 0, 0, false
		}
		start := idx + pos
		end := start + len(needle)
		leftOK := start == 0 || !unicode.IsLetter(rune(haystack[start-1]))
		rightOK := end == len(haystack) || !unicode.IsLetter(rune(haystack[end]))
		if leftOK && rightOK {
			return start, end, true
		}
		idx = start + 1
	}
}

func isTokenDelim(r rune) bool {
	switch r {
	case '/', '\\', '+', '&', ',', '(', ')', '[', ']', '{', '}', ':', ';', '|', '-':
		return true
	}
	return unicode.IsSpace(r)
}

// ParseLanguages extracts the languages advertised in a stream title such as
// "English / French", "AC3 5.1 English+French+Spanish", "Español Latino 5.1",
// or "Português (Brasil)". Codec, channel, and role tokens are filtered out,
// and results are returned in canonical BCP-47 form preserving first-seen order.
func ParseLanguages(title string) []string {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return nil
	}
	lowerBuf := []byte(strings.ToLower(trimmed))
	origBuf := []byte(trimmed)

	type hit struct {
		pos  int
		code string
	}
	var hits []hit
	seenBaseSpecific := make(map[string]struct{})

	for _, p := range titlePhrasePatterns {
		for {
			start, end, ok := findPhraseBoundary(string(lowerBuf), p.phrase)
			if !ok {
				break
			}
			hits = append(hits, hit{pos: start, code: p.code})
			base, _, _ := strings.Cut(strings.ToLower(p.code), "-")
			seenBaseSpecific[base] = struct{}{}
			for i := start; i < end && i < len(lowerBuf); i++ {
				lowerBuf[i] = ' '
				if i < len(origBuf) {
					origBuf[i] = ' '
				}
			}
		}
	}

	remaining := string(origBuf)
	start := -1
	flushToken := func(tokStart, tokEnd int) {
		rawTok := strings.Trim(remaining[tokStart:tokEnd], "._\"'")
		if rawTok == "" {
			return
		}
		lower := strings.ToLower(rawTok)
		if audioJunkTokens[lower] {
			return
		}
		var code string
		switch {
		case displayLanguageAliases[lower] != "":
			code = displayLanguageAliases[lower]
		case len(lower) == 3 && iso6392to1[lower] != "":
			code = iso6392to1[lower]
		case lower == "pob":
			code = "pt-BR"
		case len(rawTok) == 2 && rawTok == strings.ToUpper(rawTok) && validTwoLetterTokens[lower]:
			code = lower
		}
		if code == "" {
			return
		}
		base, _, hasSubtag := strings.Cut(strings.ToLower(code), "-")
		if !hasSubtag {
			if _, alreadySpecific := seenBaseSpecific[base]; alreadySpecific {
				return
			}
		}
		hits = append(hits, hit{pos: tokStart, code: code})
	}

	for i, r := range remaining {
		if isTokenDelim(r) {
			if start >= 0 {
				flushToken(start, i)
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		flushToken(start, len(remaining))
	}

	if len(hits) == 0 {
		return nil
	}
	slices.SortStableFunc(hits, func(a, b hit) int { return cmp.Compare(a.pos, b.pos) })

	seen := make(map[string]struct{}, len(hits))
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		canon := CanonicalBCP47(h.code)
		if canon == "" {
			continue
		}
		key := strings.ToLower(canon)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, canon)
	}
	return out
}

// EffectiveLanguages returns the effective BCP-47 language tag(s) for a stream
// given its container Language tag and stream Title:
//   - If lang is empty, "und", or "mul", it falls back to ParseLanguages(title).
//   - If lang is a bare primary tag (e.g. "spa"/"es", "por"/"pt", "fre"/"fr")
//     and title advertises a regional/script variant of that same primary
//     (e.g. "Español Latino" -> "es-419"), the specific tag is returned first.
//   - Otherwise, CanonicalBCP47(lang) is returned.
func EffectiveLanguages(lang, title string) []string {
	parsedLang, ok := ParseCompatible(lang)
	titleLangs := ParseLanguages(title)
	if !ok {
		return titleLangs
	}
	canon := CanonicalBCP47(lang)
	if parsedLang.Script == "" && parsedLang.Region == "" && len(titleLangs) > 0 {
		var refined []string
		for _, tl := range titleLangs {
			if pt, okT := ParseCompatible(tl); okT && pt.Primary == parsedLang.Primary && (pt.Script != "" || pt.Region != "") {
				refined = append(refined, tl)
			}
		}
		if len(refined) > 0 {
			return append(refined, canon)
		}
	}
	return []string{canon}
}

// BestStreamMatchRank returns the highest MatchRank(want, candidate) across a
// stream's EffectiveLanguages(streamLang, streamTitle).
func BestStreamMatchRank(want, streamLang, streamTitle string) int {
	best := MatchRank(want, streamLang)
	for _, cand := range EffectiveLanguages(streamLang, streamTitle) {
		if r := MatchRank(want, cand); r > best {
			best = r
		}
	}
	return best
}

// Stream role constants returned by TrackRole.
const (
	RoleMain        = "main"
	RoleCommentary  = "commentary"
	RoleDescription = "description"
)

// TrackRole classifies an audio stream's role ("main", "commentary", or
// "description") using both container disposition flags and stream title cues.
func TrackRole(title string, commentary, visualImpaired *bool) string {
	if commentary != nil && *commentary {
		return RoleCommentary
	}
	if visualImpaired != nil && *visualImpaired {
		return RoleDescription
	}
	lower := strings.ToLower(strings.TrimSpace(title))
	if lower == "" {
		return RoleMain
	}
	if strings.Contains(lower, "commentary") ||
		strings.Contains(lower, "director's comment") ||
		strings.Contains(lower, "directors comment") ||
		strings.Contains(lower, "cast comment") ||
		strings.Contains(lower, "audio comment") {
		return RoleCommentary
	}
	if strings.Contains(lower, "audio description") ||
		strings.Contains(lower, "descriptive audio") ||
		strings.Contains(lower, "visually impaired") ||
		strings.Contains(lower, "visual impaired") ||
		strings.Contains(lower, "[ad]") ||
		strings.Contains(lower, "(ad)") {
		return RoleDescription
	}
	return RoleMain
}

// StreamLangInput describes one stream for multi-audio release language inference.
type StreamLangInput struct {
	Type           string
	Language       string
	Title          string
	Commentary     *bool
	VisualImpaired *bool
}

// InferMultiAudioLanguages infers the missing language of a single untagged
// main-dialogue audio stream when a multi-language release advertises English
// plus exactly one non-English language and the other main audio stream(s) are
// already identified as English. It returns a slice parallel to streams with an
// inferred BCP-47 code for that stream (and "" for all others).
func InferMultiAudioLanguages(streams []StreamLangInput, releaseLanguages []string) []string {
	out := make([]string, len(streams))
	if len(releaseLanguages) < 2 {
		return out
	}
	var nonEnglishRelease []string
	seenRel := make(map[string]struct{})
	hasEnglishRelease := false
	for _, rl := range releaseLanguages {
		t, ok := ParseCompatible(rl)
		if !ok {
			continue
		}
		if t.Primary == "en" {
			hasEnglishRelease = true
			continue
		}
		canon := CanonicalBCP47(rl)
		key := strings.ToLower(canon)
		if _, dup := seenRel[key]; !dup {
			seenRel[key] = struct{}{}
			nonEnglishRelease = append(nonEnglishRelease, canon)
		}
	}
	if !hasEnglishRelease || len(nonEnglishRelease) != 1 {
		return out
	}

	untaggedIdx := -1
	hasEnglishTrack := false
	for i, s := range streams {
		if s.Type != "audio" || TrackRole(s.Title, s.Commentary, s.VisualImpaired) != RoleMain {
			continue
		}
		eff := EffectiveLanguages(s.Language, s.Title)
		if len(eff) == 0 {
			if untaggedIdx != -1 {
				return out // More than one untagged main audio track: ambiguous.
			}
			untaggedIdx = i
			continue
		}
		for _, code := range eff {
			if t, ok := ParseCompatible(code); ok {
				if t.Primary == "en" {
					hasEnglishTrack = true
				} else if t.Primary == strings.ToLower(strings.Split(nonEnglishRelease[0], "-")[0]) {
					return out // Non-English release language is already tagged on a stream.
				}
			}
		}
	}
	if untaggedIdx >= 0 && hasEnglishTrack {
		out[untaggedIdx] = nonEnglishRelease[0]
	}
	return out
}
