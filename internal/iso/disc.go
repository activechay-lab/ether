// Adapted from AltMount (internal/importer/archive/iso_expansion.go),
// MIT License, Copyright (c) 2025 Javier Blanco.
// https://github.com/javi11/altmount

package iso

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// discSuffixPattern matches volume labels like "AVATAR_FIRE_AND_ASH_DISC_1",
// "MOVIE-CD2", "TITLE PART 3". Group 1 is the title, group 2 the disc
// identifier (a number or a single letter).
var discSuffixPattern = regexp.MustCompile(`(?i)^(.+?)[ _.\-]*(?:disc|cd|part|d|side)[ _.\-]*([0-9]+|[a-z])$`)

// GroupKey returns the key that groups the discs of one title, and this
// disc's number. It prefers the volume label and falls back to the image's
// file name when the label is empty or has no disc suffix. A label without a
// disc suffix is its own key with disc number 0.
func GroupKey(label, filename string) (key string, disc int) {
	candidates := []string{label}
	if filename != "" {
		candidates = append(candidates, strings.TrimSuffix(filename, filepath.Ext(filename)))
	}
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if m := discSuffixPattern.FindStringSubmatch(c); m != nil {
			return normaliseKey(m[1]), discNumber(m[2])
		}
	}
	for _, c := range candidates {
		if c = strings.TrimSpace(c); c != "" {
			return normaliseKey(c), 0
		}
	}
	return "", 0
}

func normaliseKey(s string) string {
	return strings.ToUpper(strings.Trim(strings.TrimSpace(s), "_-. "))
}

// discNumber turns "1" into 1 and "A" into 1, "B" into 2.
func discNumber(s string) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	if len(s) == 1 {
		if c := strings.ToUpper(s)[0]; c >= 'A' && c <= 'Z' {
			return int(c-'A') + 1
		}
	}
	return 0
}
