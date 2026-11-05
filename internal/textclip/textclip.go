// Package textclip bounds text without splitting UTF-8 code points.
package textclip

import "unicode/utf8"

// Bytes returns the longest whole-code-point prefix within n bytes.
func Bytes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
