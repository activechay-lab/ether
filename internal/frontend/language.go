package frontend

import "github.com/braedonsaunders/ether/internal/langtag"

// NormalizeLanguage folds a client language tag to the form Ether stores.
func NormalizeLanguage(tag string) string { return langtag.Normalize(tag) }

// LanguagePrimary returns the canonical primary subtag for protocol labels.
func LanguagePrimary(tag string) (string, bool) {
	parsed, ok := langtag.ParseBCP47(tag)
	return parsed.Primary, ok
}

// LanguageMatchTier compares full and generic tags using the host's matching
// rules; two different regional variants do not match each other.
func LanguageMatchTier(a, b string) int { return langtag.MatchTier(a, b) }
