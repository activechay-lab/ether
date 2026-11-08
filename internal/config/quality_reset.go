package config

import (
	"encoding/json"
	"log/slog"
)

// preStandardProfileKeys are fields only quality profiles stored before the
// Radarr/Sonarr-standard model (docs/quality-ranking.md) carry. Releases
// before it always wrote the preference lists.
var preStandardProfileKeys = []string{"ranking", "rankingConfig", "evaluationMode", "preferredSources", "preferredCodecs", "preferredAudio", "preferLargest", "convertedRanking"}

// resetPreStandardQuality replaces the quality profiles of a stored
// document (config.json or the cluster's shared settings) with the shipped
// defaults when any profile predates the Radarr/Sonarr-standard model, and
// drops the resolver's old global format scores. Old rankings are not
// converted: an upgraded install ranks exactly as a fresh one. It reports
// whether doc changed.
func resetPreStandardQuality(doc map[string]any) bool {
	changed := false
	if res, ok := doc["resolver"].(map[string]any); ok {
		for _, k := range []string{"customFormatScores", "minCustomFormatScore"} {
			if _, ok := res[k]; ok {
				delete(res, k)
				changed = true
			}
		}
	}
	quality, _ := doc["quality"].(map[string]any)
	profiles, _ := quality["profiles"].([]any)
	for _, item := range profiles {
		p, _ := item.(map[string]any)
		if !hasAnyKey(p, preStandardProfileKeys) {
			continue
		}
		var defaults any
		if b, err := json.Marshal(DefaultQuality().Profiles); err != nil || json.Unmarshal(b, &defaults) != nil {
			return changed
		}
		quality["profiles"] = defaults
		slog.Warn("config: quality profiles predate the Radarr/Sonarr-standard ranking; reset them to the defaults")
		return true
	}
	return changed
}

func hasAnyKey(m map[string]any, keys []string) bool {
	for _, k := range keys {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}
