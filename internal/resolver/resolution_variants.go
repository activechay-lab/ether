package resolver

import "github.com/braedonsaunders/ether/internal/core"

// pickResolutionVariants first reserves one slot per available resolution,
// then distributes remaining slots in rounds. A busy 4K tier cannot crowd
// out 1080p or 720p. Ranking, profile rejection and client compatibility are
// applied by Variants before this cache-only selection.
func pickResolutionVariants(t *core.Title, profile core.QualityProfile, auto core.Release, ranked []candidate, alternatives, limit int, allowTaller bool) []core.ReleaseVariant {
	if limit <= 0 {
		return nil
	}
	limit = min(limit, core.MaxVariants)
	perResolution := 1 + max(0, min(alternatives, maxAlternativesLimit))
	buckets := map[int][]core.Release{auto.Info.Height: {auto}}
	seenID := map[string]bool{auto.ID: true}
	seenFile := map[string]bool{releaseIdentity(auto): true}
	order := []int{auto.Info.Height}
	// Prefer the familiar resolution tiers, independently of source count.
	for _, height := range []int{2160, 1080, 720} {
		if height != auto.Info.Height {
			order = append(order, height)
		}
	}
	knownHeight := map[int]bool{}
	for _, height := range order {
		knownHeight[height] = true
	}
	for _, c := range ranked {
		r := c.rel
		if seenID[r.ID] || seenFile[releaseIdentity(r)] || releaseAboveProfile(r, t, profile, allowTaller) {
			continue
		}
		seenID[r.ID], seenFile[releaseIdentity(r)] = true, true
		if !knownHeight[r.Info.Height] {
			order = append(order, r.Info.Height)
			knownHeight[r.Info.Height] = true
		}
		buckets[r.Info.Height] = append(buckets[r.Info.Height], r)
	}
	selected := map[int][]core.Release{}
	remaining := limit
	for round := 0; round < perResolution && remaining > 0; round++ {
		for _, height := range order {
			if remaining == 0 {
				break
			}
			if round >= len(buckets[height]) {
				continue
			}
			selected[height] = append(selected[height], buckets[height][round])
			remaining--
		}
	}
	out := make([]core.ReleaseVariant, 0, limit-remaining)
	for _, height := range order {
		for _, r := range selected[height] {
			isAuto := len(out) == 0
			kind := "alt"
			if isAuto {
				kind = "auto"
			}
			out = append(out, core.ReleaseVariant{ID: r.ID, Label: variantLabel(r, kind), Edition: r.Info.Edition, Auto: isAuto, Release: r, Info: estimateVariantInfo(t, sizedForEpisode(r))})
		}
	}
	return out
}
