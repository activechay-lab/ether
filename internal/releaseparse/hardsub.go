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
	t := Tokens(name)
	for i, w := range t {
		switch w {
		case "hc", "hardsub", "hardsubs", "hardcoded", "korsub", "chsub", "chs", "subbed", "hardsubbed", "hardcodedsubs", "hdtc", "hdcam", "camrip", "hchdrip":
			return true
		case "sub":
			return true
		case "cht", "vie":
			if i+1 < len(t) && t[i+1] == "sub" {
				return true
			}
		}
	}
	return false
}

// KeywordMatch matches a complete token sequence, treating release separators
// alike. The owner shorthand "sub" includes explicit hard-subtitle aliases.
func KeywordMatch(name, keyword string) bool {
	if strings.EqualFold(strings.TrimSpace(keyword), "sub") && HardSubbed(name) {
		return true
	}
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
