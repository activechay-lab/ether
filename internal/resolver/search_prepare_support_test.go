package resolver

import (
	"github.com/braedonsaunders/ether/internal/core"
)

// explicitReleasePolicy reports a profile with enabled release conditions.
// Progressive start still applies: a primary-tier healthy hit can finish
// Play without waiting out every indexer. A require-fallback stays behind
// criticalPathDeadline so a delayed primary can still arrive.
func explicitReleasePolicy(profile core.QualityProfile) bool {
	for _, r := range profile.ReleaseRules {
		if !r.Disabled && len(r.Conditions) > 0 {
			return true
		}
	}
	return false
}
