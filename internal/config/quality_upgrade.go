package config

import (
	"encoding/json"
	"maps"
	"slices"

	"github.com/braedonsaunders/ether/internal/core"
)

// upgradeShippedQuality moves an install still on the quality defaults
// releases up to v0.3.1 shipped (1080p at home and away, an Apple TV rule
// for 4K, an 8 Mbps remote cap) to the current ones: no server cap at
// home or away, each device held to what it can play. Anything the owner
// changed is left alone. It reports whether cfg changed.
func upgradeShippedQuality(cfg *Config) bool {
	q := &cfg.Quality
	if q.Default != "1080p" || q.Remote != "1080p" || len(q.Rules) != 1 || q.Rules[0] != (QualityRule{Product: "Apple TV", Profile: "4K"}) {
		return false
	}
	uhd := -1
	for i, p := range q.Profiles {
		if p.Name == "4K" {
			uhd = i
		}
	}
	if uhd < 0 {
		return false
	}
	q.Default, q.Remote, q.Rules = "4K", "", nil
	if q.Profiles[uhd].MinHeight == 1080 {
		q.Profiles[uhd].MinHeight = 720
	}
	if cfg.Transcode.RemoteMaxBitrateKbps == 8000 {
		cfg.Transcode.RemoteMaxBitrateKbps = 0
	}
	return true
}

// splitSharedProfiles gives every quality profile exactly one media. A
// profile saved before profiles were per media (v0.3.4 and earlier) served
// movies and TV shows alike; it becomes a movie profile and a TV profile
// with the same name and settings, the TV one right after it.
//
// Names are unique per media, and every reference to a profile (client
// rules, the At home and Away defaults, AI tools) is a name resolved with
// the title's media. So each reference keeps resolving to the same
// settings for movies, for shows and before the media is known, and no
// reference is rewritten. It reports whether q changed.
func splitSharedProfiles(q *Quality) bool {
	split := false
	out := make([]core.QualityProfile, 0, 2*len(q.Profiles))
	for _, p := range q.Profiles {
		if p.MediaType != core.MediaAny {
			out = append(out, p)
			continue
		}
		split = true
		for _, media := range core.ProfileMediaTypes {
			c := cloneProfile(p)
			c.MediaType = media
			out = append(out, c)
		}
	}
	if split {
		q.Profiles = out
	}
	return split
}

// cloneProfile deep-copies p field by field so the per-media copies share
// no slices or maps. It does not go through JSON, whose decoding would fill
// defaults (such as the ranking) the original left unset.
func cloneProfile(p core.QualityProfile) core.QualityProfile {
	c := p
	c.ReleaseRules = nil
	for _, r := range p.ReleaseRules {
		r.Conditions = slices.Clone(r.Conditions)
		r.Unless = slices.Clone(r.Unless)
		r.Fallback = slices.Clone(r.Fallback)
		c.ReleaseRules = append(c.ReleaseRules, r)
	}
	if p.ReleaseRules != nil && c.ReleaseRules == nil {
		c.ReleaseRules = []core.ReleaseRule{}
	}
	c.Ranking = slices.Clone(p.Ranking)
	if p.RankingConfig != nil {
		c.RankingConfig = make(map[string]json.RawMessage, len(p.RankingConfig))
		for k, v := range p.RankingConfig {
			c.RankingConfig[k] = slices.Clone(v)
		}
	}
	c.AllowedQualities = slices.Clone(p.AllowedQualities)
	c.CustomFormatScores = maps.Clone(p.CustomFormatScores)
	if p.MinCustomFormatScore != nil {
		v := *p.MinCustomFormatScore
		c.MinCustomFormatScore = &v
	}
	if p.Import != nil {
		imp := *p.Import
		imp.Groups = slices.Clone(p.Import.Groups)
		c.Import = &imp
	}
	c.PreferredSources = slices.Clone(p.PreferredSources)
	c.PreferredCodecs = slices.Clone(p.PreferredCodecs)
	c.PreferredAudio = slices.Clone(p.PreferredAudio)
	c.PreferredLanguages = slices.Clone(p.PreferredLanguages)
	return c
}
