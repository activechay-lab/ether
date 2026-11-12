package resolver

import "github.com/braedonsaunders/ether/internal/core"

// alternativeSourceOrder is the reservation priority for
// AlternativesPerSource: a Remux, then a Blu-ray ("disc") encode, then a WEB
// release (WEB-DL/WEBRip), and other sources last (only when a tier has
// nothing better). The names come from sourceClass, which classifies from the
// source facts the resolver already parses.
var alternativeSourceOrder = []string{"remux", "disc", "web", "other"}

// pickResolutionVariants first reserves one slot per available resolution,
// then distributes remaining slots in rounds. A busy 4K tier cannot crowd
// out 1080p or 720p. Ranking, profile rejection and client compatibility are
// applied by Variants before this cache-only selection.
//
// perResolution turns on the AlternativesPerResolution behaviour: the
// alternative count applies to each resolution tier. perSource turns on
// AlternativesPerSource: each tier that appears reserves one slot per source
// class (a Remux, a Blu-ray encode and a WEB release, with other sources only
// when the tier has nothing better), so a WEB-DL still shows beside a tier
// full of remuxes. When both are on, a tier's class picks come first and the
// alternative count adds further copies. Auto is unchanged either way: it is
// still the strict profile pick and heads the menu.
func pickResolutionVariants(t *core.Title, profile core.QualityProfile, auto core.Release, ranked []candidate, alternatives, limit int, allowTaller, perResolution, perSource bool, wanted []string) []core.ReleaseVariant {
	if limit <= 0 {
		return nil
	}
	limit = min(limit, core.MaxVariants)
	base := 1
	if perResolution {
		base += max(0, min(alternatives, maxAlternativesLimit))
	}
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

	// seqs holds each tier's ordered choices. Without perSource that is just
	// the rank-ordered bucket; maxRounds then limits how many copies a tier
	// may take. With perSource the class reservations come first and set a
	// tier's minimum width, wrapping in the alternative count when it also
	// applies per resolution.
	seqs := map[int][]core.Release{}
	maxRounds := base
	for _, height := range order {
		bucket := buckets[height]
		if !perSource {
			seqs[height] = bucket
			continue
		}
		seq, reps := sourceVariantSequence(bucket, auto, height == auto.Info.Height, perResolution)
		seqs[height] = seq
		if reps > maxRounds {
			maxRounds = reps
		}
	}

	selected := map[int][]core.Release{}
	remaining := limit
	for round := 0; round < maxRounds && remaining > 0; round++ {
		for _, height := range order {
			if remaining == 0 {
				break
			}
			if round >= len(seqs[height]) {
				continue
			}
			selected[height] = append(selected[height], seqs[height][round])
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
			out = append(out, core.ReleaseVariant{ID: r.ID, Label: variantLabel(r, kind), Edition: r.Info.Edition, Auto: isAuto, Release: r, Info: estimateVariantInfo(t, sizedForEpisode(r), wanted)})
		}
	}
	return out
}

// sourceVariantSequence orders one resolution tier's releases for
// AlternativesPerSource: Auto first in its own tier, then at most one release
// per source class in priority order, and (when the alternative count also
// applies per resolution) the remaining releases by rank. It reports the
// width of the class reservations (Auto included in its own tier), which sets
// that tier's minimum width. Other sources (HDTV, DVD, CAM) are reserved only
// when the tier has no Remux, Blu-ray or WEB release.
func sourceVariantSequence(bucket []core.Release, auto core.Release, autoTier, fill bool) ([]core.Release, int) {
	var seq []core.Release
	used := map[string]bool{}
	if autoTier {
		seq = append(seq, auto)
		used[auto.ID] = true
	}
	autoClass := sourceClass(auto.Info.Source)
	hasKnown := false
	for _, r := range bucket {
		if sourceClass(r.Info.Source) != "other" {
			hasKnown = true
			break
		}
	}
	for _, class := range alternativeSourceOrder {
		if class == "other" && hasKnown {
			continue // HDTV etc. only when the tier has nothing better
		}
		if autoTier && class == autoClass {
			continue // Auto already represents this class in its tier
		}
		for _, r := range bucket {
			if used[r.ID] {
				continue
			}
			if sourceClass(r.Info.Source) == class {
				seq = append(seq, r)
				used[r.ID] = true
				break
			}
		}
	}
	reps := len(seq)
	if fill {
		for _, r := range bucket {
			if !used[r.ID] {
				seq = append(seq, r)
				used[r.ID] = true
			}
		}
	}
	return seq, reps
}
