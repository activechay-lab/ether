package releaseparse

import (
	"regexp"
	"strings"
)

var disclaimerNameWord = regexp.MustCompile(`^[\pL\pN]+$`)

// DisclaimsDirector recognizes a narrowly phrased post-year disclaimer. It
// requires the complete director name: aliases and initials are not evidence.
func DisclaimsDirector(name, director string) bool {
	words := strings.Fields(spacify(director))
	if len(words) < 2 {
		return false
	}
	spaced := spacify(name)
	_, yearAt := parseYearAt(spaced)
	if yearAt < 0 {
		return false
	}
	tail := strings.Fields(spaced[yearAt+4:])
	if len(tail) < len(words)+3 || !strings.EqualFold(tail[0], "not") || !strings.EqualFold(tail[1], "the") {
		return false
	}
	for i, word := range words {
		if !disclaimerNameWord.MatchString(word) || !strings.EqualFold(word, tail[i+2]) {
			return false
		}
	}
	return strings.EqualFold(tail[len(words)+2], "film")
}

// IsPlaceholderResult reports synthetic "no results" rows returned by
// Torznab/Jackett/Prowlarr bridges when an indexer refuses a text search
// (for example, "No results! Make sure to use the IMDb ID to search").
// Real releases carrying technical metadata, years or episode markers are kept.
func IsPlaceholderResult(name string) bool {
	s := strings.TrimSpace(name)
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	placeholder := lower == "no results" ||
		lower == "no results found" ||
		strings.HasPrefix(lower, "no results!") ||
		strings.Contains(lower, "make sure to use the imdb id")
	if !placeholder {
		return false
	}
	info := Parse(s)
	return !HasUsableInfo(info) && info.Year == 0 && len(info.Seasons) == 0 && len(info.Episodes) == 0 && !info.FullSeason
}
