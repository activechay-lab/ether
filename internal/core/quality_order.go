package core

import (
	"fmt"
	"strings"
)

// qualityOrder sources: the release types plus "unknown" (a release that
// names a resolution but no source).
var qualitySources = map[string]bool{"remux": true, "bluray": true, "webdl": true, "webrip": true, "hdtv": true, "dvd": true, "unknown": true, "cam": true, "screener": true}

// QualityOrderError checks a profile's quality order: at most 64 items of 1
// to 64 "<source>-<height>" qualities, each quality in one item only. An
// empty order ranks every quality alike.
func QualityOrderError(order []QualityItem) string {
	if len(order) > 64 {
		return "at most 64 quality entries"
	}
	seen := map[string]bool{}
	for i, item := range order {
		if len(item.Qualities) == 0 || len(item.Qualities) > 64 {
			return fmt.Sprintf("entry %d must list 1 to 64 qualities", i+1)
		}
		for _, q := range item.Qualities {
			src, h, ok := strings.Cut(q, "-")
			height := 0
			if ok {
				_, err := fmt.Sscanf(h, "%d", &height)
				ok = err == nil && fmt.Sprint(height) == h && height > 0 && height <= 4320
			}
			if !ok || !qualitySources[src] {
				return fmt.Sprintf("%q is not a quality", q)
			}
			if seen[q] {
				return fmt.Sprintf("%q is listed twice", q)
			}
			seen[q] = true
		}
	}
	return ""
}
