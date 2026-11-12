// Package language exposes the shared language-tag validation to frontends.
package language

import "github.com/braedonsaunders/ether/internal/langtag"

// Valid reports whether value is an accepted BCP 47 language tag.
func Valid(value string) bool {
	_, ok := langtag.ParseBCP47(value)
	return ok
}

// Canonical folds any accepted spelling of a language — ISO 639-1 ("pt"),
// ISO 639-2 ("por"), BCP 47 with any region casing ("pt-BR", "pt_br",
// "PT-br"), a display name ("Portuguese", "Português") or a parser name
// ("portuguese-brazil") — to one canonical BCP 47 tag, preserving a known
// region where the tag carries one: "pt", "por", "Portuguese" and
// "Português" fold to "pt", while "pt-BR", "pt_br" and "Brazilian
// Portuguese" keep "pt-BR". The primary subtag is lower-cased and
// script/region are title-cased ("es-419", "fr-CA", "zh-Hant-TW"). An
// unknown value returns "".
func Canonical(value string) string {
	return langtag.CanonicalBCP47(value)
}

// Base is Canonical without any script or region subtag: the ISO 639-1
// primary ("pt" for every spelling of Portuguese). "" when unknown.
func Base(value string) string {
	tag, ok := langtag.ParseCompatible(value)
	if !ok {
		return ""
	}
	return tag.Primary
}
