package subtitles

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"

	"github.com/braedonsaunders/ether/internal/frontend/kit/subtext"
	"github.com/braedonsaunders/ether/internal/langtag"
)

// Cue is one subtitle cue.
type Cue = subtext.Cue

// legacyCharmap is the Windows code page legacy subtitle files in a
// language are almost always encoded in.
func legacyCharmap(lang string) *charmap.Charmap {
	switch langtag.Normalize(lang) {
	case "ru", "uk", "bg", "sr", "mk", "be":
		return charmap.Windows1251
	case "pl", "cs", "sk", "hu", "ro", "hr", "sl", "bs", "sq":
		return charmap.Windows1250
	case "el":
		return charmap.Windows1253
	case "tr", "az":
		return charmap.Windows1254
	case "he":
		return charmap.Windows1255
	case "ar", "fa", "ur":
		return charmap.Windows1256
	case "et", "lv", "lt":
		return charmap.Windows1257
	case "vi":
		return charmap.Windows1258
	case "th":
		return charmap.Windows874
	}
	return charmap.Windows1252
}

func toUTF8Lang(b []byte, lang string) string {
	switch {
	case len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		return string(b[3:])
	case len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE:
		return decodeUTF16(b[2:], true)
	case len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		return decodeUTF16(b[2:], false)
	}
	// BOM-less UTF-16 heuristic: ASCII text has a NUL in every other byte.
	if len(b) >= 4 {
		le, be := 0, 0
		n := min(len(b), 512)
		for i := 0; i+1 < n; i += 2 {
			if b[i] != 0 && b[i+1] == 0 {
				le++
			}
			if b[i] == 0 && b[i+1] != 0 {
				be++
			}
		}
		if le > 10 && le > be*4 {
			return decodeUTF16(b, true)
		}
		if be > 10 && be > le*4 {
			return decodeUTF16(b, false)
		}
	}
	if utf8.Valid(b) {
		return RepairMojibake(string(b))
	}
	out, err := legacyCharmap(lang).NewDecoder().Bytes(b)
	if err != nil {
		return strings.ToValidUTF8(string(b), string(utf8.RuneError))
	}
	return RepairMojibake(string(out))
}

// mojibakeRe matches UTF-8 that was decoded as Windows-1252/Latin-1 and
// saved again (â™ª for ♪, Ã© for é).
var mojibakeRe = regexp.MustCompile(`(?:Ã[\x80-\xBF]|Â[\x80-\xBF]|â[€™œž‘’“”]|ðŸ|Ã[©®±¼½¾]|â™)`)

// HasMojibake reports UTF-8 that still looks like a 1252 mis-decode.
// Only the two-byte pair patterns count: a single Å/Ä/Ã is a real
// Nordic or German letter, not mojibake.
func HasMojibake(s string) bool {
	return mojibakeRe.MatchString(s)
}

func mojibakeScore(s string) int {
	n := 0
	for _, r := range s {
		switch {
		case r == 'Ã' || r == 'Â' || r == 'Å' || r == 'Ä':
			n += 2
		case r == 'â' || r == 'ð' || r == 'ï':
			n++
		case r == '\uFFFD':
			n += 3
		}
	}
	return n
}

// RepairMojibake undoes UTF-8 that was read as Windows-1252 or Latin-1
// and written back as UTF-8. Unchanged when the reverse is not valid
// UTF-8 or does not look cleaner.
func RepairMojibake(s string) string {
	if s == "" || !HasMojibake(s) {
		return s
	}
	if out, ok := undoMojibake(s, charmap.Windows1252); ok {
		return out
	}
	if out, ok := undoMojibake(s, charmap.ISO8859_1); ok {
		return out
	}
	return s
}

func undoMojibake(s string, page *charmap.Charmap) (string, bool) {
	raw := make([]byte, 0, len(s))
	for _, r := range s {
		b, ok := page.EncodeRune(r)
		if !ok {
			return "", false
		}
		raw = append(raw, b)
	}
	if !utf8.Valid(raw) {
		return "", false
	}
	out := string(raw)
	if mojibakeScore(out) >= mojibakeScore(s) || HasMojibake(out) && mojibakeScore(out) > 0 {
		return "", false
	}
	return out, true
}

func decodeUTF16(b []byte, littleEndian bool) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		if littleEndian {
			u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
		} else {
			u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
		}
	}
	return string(utf16.Decode(u))
}

var (
	tagRe     = regexp.MustCompile(`\{\\[^}]*\}`)
	htmlTagRe = regexp.MustCompile(`</?([a-zA-Z]+)[^>]*>`)
)

// ParseCues parses SRT or WebVTT text (already UTF-8) into cues.
func ParseCues(text string) ([]Cue, error) { return subtext.ParseCues(text) }

// RenderSRT renders cues as SubRip.
func RenderSRT(cues []Cue) string { return subtext.RenderSRT(cues) }

// RenderVTT renders cues as WebVTT.
func RenderVTT(cues []Cue) string { return subtext.RenderVTT(cues) }

// ASSHeader is the script header RenderASS writes: FFmpeg's own header for
// a converted text track (PlayRes 384x288, one "Default" style), so a
// player that restyles "Default" (Plex Web's subtitle colour, size and
// position settings) treats it like the converted stream PMS sends.
const ASSHeader = subtext.ASSHeader

// RenderASS renders cues as an ASS script with ASSHeader: <i>, <b> and <u>
// become override tags, other markup is dropped and line breaks are \N.
func RenderASS(cues []Cue) string { return subtext.RenderASS(cues) }

// assTS is an ASS timestamp, H:MM:SS.cc.
func assTS(d time.Duration) string { return subtext.FormatASSTimestamp(d) }

// NormalizeLang is Normalize with the subtitle's language, which picks the
// legacy code page for non-UTF files (Windows-1251 for Russian, …).
func NormalizeLang(raw []byte, lang string) (srt, vtt string, err error) {
	text := toUTF8Lang(raw, lang)
	cues, err := ParseCues(text)
	if err != nil && strings.Contains(strings.ToLower(text), "[events]") {
		cues, err = ParseASS(text)
	}
	if err != nil {
		return "", "", err
	}
	return RenderSRT(cues), RenderVTT(cues), nil
}
