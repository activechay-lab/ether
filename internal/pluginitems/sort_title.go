package pluginitems

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// SortTitle derives the normalized lowercase sort key for a display title,
// matching catalog.SortTitle rules (spec §3.2): strips invisible format
// characters, removes a leading English article ("the ", "a ", "an "), and
// folds Latin accents/diacritics and ligatures to base ASCII letters.
func SortTitle(s string) string {
	l := strings.ToLower(strings.TrimSpace(stripFormatRunes(s)))
	for _, p := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(l, p) {
			if rest := strings.TrimSpace(l[len(p):]); rest != "" {
				l = rest
			}
			break
		}
	}
	return FoldSortTitle(l)
}

// FoldSortTitle normalizes an explicit sort title to lowercase with Latin
// diacritics and ligatures folded to their base ASCII letters.
func FoldSortTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(stripFormatRunes(s)))
	if s == "" || isASCII(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	lastLatin := false
	for _, r := range s {
		if unicode.Is(unicode.Cf, r) {
			continue
		}
		if lastLatin && (unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r)) {
			continue
		}
		switch r {
		case 'æ':
			b.WriteString("ae")
			lastLatin = true
			continue
		case 'œ':
			b.WriteString("oe")
			lastLatin = true
			continue
		case 'ß':
			b.WriteString("ss")
			lastLatin = true
			continue
		case 'ø':
			b.WriteByte('o')
			lastLatin = true
			continue
		case 'ł':
			b.WriteByte('l')
			lastLatin = true
			continue
		case 'đ', 'ð':
			b.WriteByte('d')
			lastLatin = true
			continue
		case 'þ':
			b.WriteString("th")
			lastLatin = true
			continue
		case 'ı':
			b.WriteByte('i')
			lastLatin = true
			continue
		}
		if unicode.Is(unicode.Latin, r) {
			lastLatin = true
			if r < 0x80 {
				b.WriteByte(byte(r))
				continue
			}
			for _, dr := range norm.NFD.String(string(r)) {
				if unicode.Is(unicode.Mn, dr) || unicode.Is(unicode.Mc, dr) || unicode.Is(unicode.Me, dr) {
					continue
				}
				b.WriteRune(dr)
			}
			continue
		}
		lastLatin = false
		b.WriteRune(r)
	}
	return b.String()
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func stripFormatRunes(s string) string {
	if isASCII(s) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}
