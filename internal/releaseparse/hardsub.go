package releaseparse

import (
	"strings"
	"unicode"
)

// Tokens splits release punctuation without matching parts of words.
func Tokens(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// HardSubbed identifies explicit burned-in subtitle tags. Optional subtitle
// tracks (Subtitles, SUBPACK, MULTISUB) do not constitute this evidence.
func HardSubbed(name string) bool {
	tokens := Tokens(name)
	metadata := -1
	source := false
	for i, w := range tokens {
		if metadata < 0 && (len(w) == 4 && w >= "1900" && w <= "2099" || w == "720p" || w == "1080p" || w == "2160p") {
			metadata = i
		}
		switch w {
		case "hdrip", "webrip", "webdl", "web", "bluray", "brrip", "bdrip", "dvdrip", "hdtv", "hdtc", "hdcam", "cam":
			source = metadata >= 0 && i > metadata || source
		}
	}
	for i, w := range tokens {
		switch w {
		case "hardsub", "hardsubs", "hardcoded", "hardsubbed", "korsub":
			return true
		case "hc":
			paired := i+1 < len(tokens) && (tokens[i+1] == "hdrip" || tokens[i+1] == "webrip")
			if paired || metadata >= 0 && i > metadata && source {
				return true
			}
		}
	}
	return false
}

// KeywordMatch matches a complete token sequence, treating release separators
// alike. Subtitle keywords have no hidden aliases.
func KeywordMatch(name, keyword string) bool {
	n, k := Tokens(name), Tokens(keyword)
	if len(k) == 0 {
		return false
	}
	for i := 0; i+len(k) <= len(n); i++ {
		if strings.Join(n[i:i+len(k)], " ") == strings.Join(k, " ") {
			return true
		}
	}
	return false
}
