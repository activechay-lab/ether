package releaseparse

import (
	"math"
	"strconv"
	"strings"
	"unicode"
)

// ParseSize reads sizes such as "1.4 GiB", "700 MB", "1,234.5 KB",
// "1.234,5 MB" or raw byte counts. Like Cardigann, decimal and binary
// units are both treated as powers of 1024. Unparseable input is 0.
func ParseSize(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	var num strings.Builder
	var unit strings.Builder
	seenDigit := false
	for _, r := range s {
		switch {
		case unicode.IsDigit(r) || r == '.' || r == ',':
			if unit.Len() > 0 {
				// Digits after the unit ("1.5 GB (1610612736 bytes)") end the value.
				goto done
			}
			num.WriteRune(r)
			if unicode.IsDigit(r) {
				seenDigit = true
			}
		case unicode.IsLetter(r):
			if seenDigit {
				unit.WriteRune(unicode.ToLower(r))
			}
		case unicode.IsSpace(r) || r == ' ':
			if unit.Len() > 0 {
				goto done
			}
		}
	}
done:
	raw := num.String()
	// A lone comma followed by three digits groups thousands; a lone dot
	// remains a decimal point. Mixed separators use the last as decimal.
	if strings.Count(raw, ",") == 1 && !strings.Contains(raw, ".") && len(raw)-strings.IndexByte(raw, ',')-1 == 3 {
		raw = strings.ReplaceAll(raw, ",", "")
	}
	val := strings.ReplaceAll(raw, ",", ".")
	if strings.Count(val, ".") > 1 {
		last := strings.LastIndexByte(val, '.')
		val = strings.ReplaceAll(val[:last], ".", "") + val[last:]
	}
	// "1.234" with a byte unit or none and exactly three decimals after a
	// single separator is ambiguous; Cardigann reads it as a decimal.
	f, err := strconv.ParseFloat(val, 64)
	if err != nil || f < 0 {
		return 0
	}
	mult := 1.0
	switch u := unit.String(); {
	case strings.HasPrefix(u, "t") || strings.HasPrefix(u, "т"):
		mult = 1 << 40
	case strings.HasPrefix(u, "g") || strings.HasPrefix(u, "г"):
		mult = 1 << 30
	case strings.HasPrefix(u, "m") || strings.HasPrefix(u, "м"):
		mult = 1 << 20
	case strings.HasPrefix(u, "k") || strings.HasPrefix(u, "к"):
		mult = 1 << 10
	}
	v := f * mult
	if v > math.MaxInt64/2 {
		return 0
	}
	return int64(math.Round(v))
}
