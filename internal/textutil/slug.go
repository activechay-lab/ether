package textutil

import "strings"

// Slug returns a bounded ASCII identifier with no leading or trailing dash.
func Slug(s, fallback string, limit int) string {
	var b strings.Builder
	dash := true
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = fallback
	}
	if limit > 0 && len(out) > limit {
		out = strings.TrimRight(out[:limit], "-")
	}
	return out
}
