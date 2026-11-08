package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// Conversion of quality profiles stored by v0.3.5 and earlier, which ranked
// releases with Ether's own "What matters most" ladder and preference lists,
// into the Radarr/Sonarr-standard model: a quality order plus custom format
// scores (docs/quality-ranking.md). It runs once on the stored document,
// before decoding, and is deterministic. Where the standard order can
// express the old ladder the conversion is exact; where it can't, a note
// names what now ranks differently.

// legacyRankingKeys are the profile fields the conversion consumes.
var legacyRankingKeys = []string{"ranking", "rankingConfig", "evaluationMode", "preferredSources", "preferredCodecs", "preferredAudio", "preferLargest"}

// Old ladder steps, in their default order.
const (
	stepLanguage    = "language"
	stepConnection  = "connection"
	stepResolution  = "resolution"
	stepReleaseType = "releaseType"
	stepHDR         = "hdr"
	stepCustomScore = "customScore"
	stepVerified    = "verified"
	stepVideoCodec  = "videoCodec"
	stepAudioFormat = "audioFormat"
	stepSize        = "size"
)

var legacyDefaultSteps = []string{stepLanguage, stepConnection, stepResolution, stepReleaseType, stepHDR, stepCustomScore, stepVerified, stepVideoCodec, stepAudioFormat, stepSize}

// RankingSources are the release types a resolution group lists.
var RankingSources = []string{"remux", "bluray", "webdl", "webrip", "hdtv", "dvd", "unknown"}

type legacyRule struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Action   string `json:"action"`
	Disabled bool   `json:"disabled"`
}

type legacyProfile struct {
	Name                 string                     `json:"name"`
	Ranking              []string                   `json:"ranking"`
	RankingConfig        map[string]json.RawMessage `json:"rankingConfig"`
	EvaluationMode       string                     `json:"evaluationMode"`
	QualityOrder         json.RawMessage            `json:"qualityOrder"`
	PreferredSources     []core.Source              `json:"preferredSources"`
	PreferredCodecs      []string                   `json:"preferredCodecs"`
	PreferredAudio       []string                   `json:"preferredAudio"`
	PreferLargest        bool                       `json:"preferLargest"`
	PreferredLanguages   []string                   `json:"preferredLanguages"`
	MaxHeight            int                        `json:"maxHeight"`
	MinHeight            int                        `json:"minHeight"`
	AllowHDR             bool                       `json:"allowHdr"`
	AllowDolbyVision     bool                       `json:"allowDolbyVision"`
	AllowCam             bool                       `json:"allowCam"`
	CustomFormatScores   map[string]int             `json:"customFormatScores"`
	MinCustomFormatScore *int                       `json:"minCustomFormatScore"`
	ReleaseRules         []legacyRule               `json:"releaseRules"`
}

type legacyFormat struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Scores map[string]int `json:"scores"`
}

// legacyResolver is what the conversion reads from the resolver section.
type legacyResolver struct {
	CustomFormats        []legacyFormat `json:"customFormats"`
	CustomFormatScores   map[string]int `json:"customFormatScores"`
	MinCustomFormatScore *int           `json:"minCustomFormatScore"`
}

// upgradeRankingDocument converts every stored profile in doc (a config
// document, or the cluster's shared settings document) and moves the
// resolver's global custom format scores and minimum onto the profiles. It
// reports whether doc changed and a note per behaviour the standard order
// can't reproduce exactly.
func upgradeRankingDocument(doc map[string]any) (bool, []string) {
	resolverDoc, _ := doc["resolver"].(map[string]any)
	var res legacyResolver
	if resolverDoc != nil {
		remarshal(resolverDoc, &res)
	}
	changed := false
	var notes []string
	if quality, ok := doc["quality"].(map[string]any); ok {
		profiles, _ := quality["profiles"].([]any)
		for _, item := range profiles {
			p, ok := item.(map[string]any)
			if !ok || !legacyProfileDoc(p) {
				continue
			}
			for _, n := range convertLegacyProfile(p, res) {
				notes = append(notes, n)
			}
			changed = true
		}
	}
	if resolverDoc != nil {
		for _, k := range []string{"customFormatScores", "minCustomFormatScore"} {
			if _, ok := resolverDoc[k]; ok {
				delete(resolverDoc, k)
				changed = true
			}
		}
	}
	return changed, notes
}

// logRankingNotes reports, once at conversion, what the standard order ranks
// differently for a converted profile.
func logRankingNotes(notes []string) {
	for _, n := range notes {
		slog.Warn("config: converted to Radarr/Sonarr-standard ranking: " + n)
	}
}

// legacyProfileDoc reports whether a stored profile predates the standard
// model: it still carries a ladder field or preference list, it has no
// quality order field at all (this version always stores one, empty
// included), or its order is the per-group string lists PR #355 stored.
func legacyProfileDoc(p map[string]any) bool {
	for _, k := range legacyRankingKeys {
		if _, ok := p[k]; ok {
			return true
		}
	}
	raw, ok := p["qualityOrder"]
	if !ok {
		return true
	}
	order, _ := raw.([]any)
	if len(order) == 0 {
		return false
	}
	_, nested := order[0].([]any)
	return nested
}

func remarshal(in any, out any) {
	b, err := json.Marshal(in)
	if err == nil {
		_ = json.Unmarshal(b, out)
	}
}

// convertLegacyProfile rewrites one stored profile in place.
func convertLegacyProfile(doc map[string]any, res legacyResolver) []string {
	var p legacyProfile
	remarshal(doc, &p)
	conv := convertProfile(p, res)
	for _, k := range legacyRankingKeys {
		delete(doc, k)
	}
	doc["qualityOrder"] = toAny(conv.order)
	if conv.flat {
		doc["convertedRanking"] = true
	} else {
		delete(doc, "convertedRanking")
	}
	if len(conv.scores) > 0 {
		doc["customFormatScores"] = toAny(conv.scores)
	} else {
		delete(doc, "customFormatScores")
	}
	if conv.min != nil {
		doc["minCustomFormatScore"] = *conv.min
	} else {
		delete(doc, "minCustomFormatScore")
	}
	if len(conv.plugins) > 0 {
		doc["pluginScores"] = toAny(conv.plugins)
	}
	name := p.Name
	if name == "" {
		name = "(unnamed)"
	}
	out := make([]string, len(conv.notes))
	for i, n := range conv.notes {
		out[i] = fmt.Sprintf("quality profile %q: %s", name, n)
	}
	return out
}

func toAny(v any) any {
	var out any
	remarshal(v, &out)
	return out
}

// profileConversion is a converted profile's ranking definition.
type profileConversion struct {
	order   []core.QualityItem
	flat    bool // the order is ConvertedOrderGroup, resolution scored as formats
	scores  map[string]int
	min     *int
	plugins map[string]json.RawMessage
	notes   []string
}

// legacyLadder is the old EffectiveRanking: Prefer rules first unless
// placed, the owner's order, and omitted built-in steps in their default
// relative positions (a missing custom score directly before verified).
func legacyLadder(p legacyProfile) []string {
	prefer := []string{}
	valid := map[string]bool{}
	for _, r := range p.ReleaseRules {
		if r.Action == "prefer" && r.ID != "" && !valid["rule:"+r.ID] {
			valid["rule:"+r.ID] = true
			prefer = append(prefer, "rule:"+r.ID)
		}
	}
	if len(p.Ranking) == 0 {
		return append(prefer, legacyDefaultSteps...)
	}
	mentioned := map[string]bool{}
	var filtered []string
	for _, step := range p.Ranking {
		if mentioned[step] {
			continue
		}
		switch {
		case strings.HasPrefix(step, "rule:"):
			if !valid[step] {
				continue
			}
		case core.IsPluginRankingStep(step), slices.Contains(legacyDefaultSteps, step):
		default:
			continue
		}
		mentioned[step] = true
		filtered = append(filtered, step)
	}
	var out []string
	for _, k := range prefer {
		if !mentioned[k] {
			mentioned[k] = true
			out = append(out, k)
		}
	}
	out = append(out, filtered...)
	for bi, b := range legacyDefaultSteps {
		if mentioned[b] {
			continue
		}
		if b == stepCustomScore {
			if i := slices.Index(out, stepVerified); i >= 0 {
				out = slices.Insert(out, i, b)
				continue
			}
		}
		inserted := false
		for i, step := range out {
			if si := slices.Index(legacyDefaultSteps, step); si > bi {
				out = slices.Insert(out, i, b)
				inserted = true
				break
			}
		}
		if !inserted {
			out = append(out, b)
		}
	}
	return out
}

// level is one lexicographic tier of the converted scores: formats and how
// many steps of the level's weight each is worth. A level's weight is
// worth more than everything the levels below it can add up to.
type level struct {
	units map[string]int // format id → multiples of the level's weight
	span  int            // the most multiples two releases can differ by
}

// rankUnits gives each known value of a preference list (best first) the
// old indexRank, len(list)-i at its first position, times per.
func rankUnits[T comparable](list []T, known []T, id func(T) string, per int) map[string]int {
	out := map[string]int{}
	for i, v := range list {
		if !slices.Contains(known, v) || slices.Index(list, v) != i {
			continue
		}
		out[id(v)] = per * (len(list) - i)
	}
	return out
}

func maxUnit(units map[string]int) int {
	m := 0
	for _, u := range units {
		m = max(m, abs(u))
	}
	return m
}

func convertProfile(p legacyProfile, res legacyResolver) profileConversion {
	conv := profileConversion{scores: map[string]int{}}
	ladder := legacyLadder(p)
	strict := p.EvaluationMode == "servarr" && len(p.QualityOrder) > 0
	if strict {
		// Imported under PR #355's Servarr evaluation: its quality order and
		// scores are already the standard; only the streaming formats are
		// added, as on any import.
		var nested [][]string
		var items []core.QualityItem
		if json.Unmarshal(p.QualityOrder, &nested) == nil {
			for _, g := range nested {
				items = append(items, core.QualityItem{Qualities: g})
			}
		} else {
			_ = json.Unmarshal(p.QualityOrder, &items)
		}
		conv.order = items
		for k, v := range p.CustomFormatScores {
			conv.scores[k] = v
		}
		for k, v := range core.StreamingFormatScores() {
			if _, ok := conv.scores[k]; !ok {
				conv.scores[k] = v
			}
		}
		conv.min = p.MinCustomFormatScore
		if conv.min == nil {
			zero := 0
			conv.min = &zero
		}
		return conv
	}

	// Gates, tie-breaks and plugin steps leave the ladder.
	var steps []string
	pos := map[string]int{}
	for i, s := range ladder {
		pos[s] = i
	}
	for _, s := range ladder {
		switch {
		case s == stepLanguage, s == stepConnection, s == stepVerified:
		case core.IsPluginRankingStep(s):
			if conv.plugins == nil {
				conv.plugins = map[string]json.RawMessage{}
			}
			cfg := p.RankingConfig[s]
			if len(cfg) == 0 {
				cfg = json.RawMessage(`{}`)
			}
			conv.plugins[s] = cfg
			conv.notes = append(conv.notes, fmt.Sprintf("plugin step %s now adds its points to the custom format score", s))
		default:
			steps = append(steps, s)
		}
	}
	gateMoved, ruleFirst := false, false
	for _, g := range []string{stepLanguage, stepConnection} {
		for _, s := range ladder[:pos[g]] {
			switch {
			case strings.HasPrefix(s, "rule:"):
				ruleFirst = true
			case s != stepLanguage && s != stepConnection:
				gateMoved = true
			}
		}
	}
	if gateMoved {
		conv.notes = append(conv.notes, "audio language and connection fit now always rank first")
	}
	if ruleFirst {
		conv.notes = append(conv.notes, "audio language and connection fit now rank before Prefer rules")
	}
	if pos[stepVerified] < pos[stepCustomScore] {
		conv.notes = append(conv.notes, "played fine recently now only breaks ties")
	}
	defaultTail := len(ladder) >= 3 && ladder[len(ladder)-3] == stepVideoCodec && ladder[len(ladder)-2] == stepAudioFormat && ladder[len(ladder)-1] == stepSize
	if p.PreferLargest || (!defaultTail && slices.Index(ladder, stepSize) < len(ladder)-1) {
		conv.notes = append(conv.notes, "larger files now compare after the custom format score")
	}

	// The quality order. The standard ranks quality first. A ladder that
	// ranked resolution first becomes one group per resolution; release
	// type then resolution becomes each preferred type's resolutions in
	// turn. A ladder that ranked anything else above resolution (Prefer
	// rules do by default) becomes one group of every quality, with
	// resolution scored as a format at its old place, so it ranks exactly as
	// it did.
	mode := orderByResolution
	switch {
	case len(steps) > 0 && steps[0] == stepResolution:
	case len(steps) >= 2 && steps[0] == stepReleaseType && steps[1] == stepResolution:
		mode = orderBySource
	case len(steps) > 0:
		mode = orderFlat
		conv.flat = true
		conv.notes = append(conv.notes, fmt.Sprintf("kept %s above resolution: every quality is one group and resolution is scored as a format", stepName(steps[0])))
	}
	sources := knownSources(p.PreferredSources)
	conv.order = qualityOrder(p, sources, mode)

	// Levels above the bundle in ladder order, then the bundle at weight 1.
	var levels []level
	codecMoved, audioMoved, pastScore := false, false, false
	for _, s := range steps {
		before := len(levels)
		switch {
		case strings.HasPrefix(s, "rule:"):
			levels = append(levels, level{units: map[string]int{s: 1}, span: 1})
		case s == stepResolution:
			if mode == orderFlat {
				lv := level{units: map[string]int{}}
				for i, h := range core.FormatHeights {
					if (p.MaxHeight == 0 || h <= p.MaxHeight) && h >= p.MinHeight {
						lv.units[core.ResolutionFormat(h)] = len(core.FormatHeights) - i
					}
				}
				lv.span = maxUnit(lv.units)
				levels = append(levels, lv)
			}
			// Within a resolution the old ladder ranked a foreign title's
			// release in the viewer's language first, then a correctly
			// labelled series year.
			levels = append(levels,
				level{units: map[string]int{core.FormatViewerLanguage: 1}, span: 1},
				level{units: map[string]int{core.FormatSeriesYearDiffers: -1}, span: 1})
		case s == stepReleaseType:
			if mode == orderBySource {
				continue
			}
			lv := level{units: rankUnits(p.PreferredSources, core.FormatSources, func(s core.Source) string { return core.SourceFormat(s) }, 1)}
			lv.span = maxUnit(lv.units)
			levels = append(levels, lv)
		case s == stepHDR:
			// Dolby Vision ranked 2, HDR 1: a Dolby Vision release matches both.
			levels = append(levels, level{units: map[string]int{core.FormatDolbyVision: 1, core.FormatHDR: 1}, span: 2})
		case s == stepVideoCodec && !defaultTail:
			codecMoved = true
			lv := level{units: rankUnits(p.PreferredCodecs, core.FormatCodecs, core.CodecFormat, 1)}
			lv.span = maxUnit(lv.units)
			levels = append(levels, lv)
		case s == stepAudioFormat && !defaultTail:
			audioMoved = true
			// The old audio rank: twice the format's preference, plus one for Atmos.
			lv := level{units: rankUnits(p.PreferredAudio, core.FormatAudioCodecs, core.AudioFormat, 2)}
			lv.span = maxUnit(lv.units) + 1
			lv.units[core.FormatAtmos] = 1
			levels = append(levels, lv)
		case s == stepCustomScore:
			pastScore = true
		}
		if pastScore && len(levels) > before {
			conv.notes = append(conv.notes, fmt.Sprintf("%s now ranks above the custom score", stepName(s)))
		}
	}

	// The bundle: the old total score's parts that are preferences, at
	// their old points, plus the owner's custom formats at face value.
	bundle := map[string]int{}
	if !codecMoved {
		for id, u := range rankUnits(p.PreferredCodecs, core.FormatCodecs, core.CodecFormat, 6) {
			bundle[id] = u
		}
	}
	if !audioMoved {
		for id, u := range rankUnits(p.PreferredAudio, core.FormatAudioCodecs, core.AudioFormat, 4) {
			bundle[id] = u
		}
		bundle[core.FormatAtmos] = 3
	}
	for _, s := range core.FormatSources {
		if !slices.Contains(sources, string(s)) {
			bundle[core.SourceFormat(s)] = 3
		}
	}
	if p.AllowHDR {
		bundle[core.FormatHDR10] = 8
	}
	bundle[core.FormatRepackProper] = 6
	bundle[core.FormatLowBitrate] = -60
	if len(p.PreferredLanguages) > 0 {
		bundle[core.FormatPreferredLanguage] = 5
		bundle[core.FormatPreferredLanguageRegion] = 2
		bundle[core.FormatOtherLanguage] = -25
		bundle[core.FormatPreferredSubtitles] = 2
	}
	for k, v := range core.StreamingFormatScores() {
		if k != core.FormatSeriesYearDiffers {
			bundle[k] = v
		}
	}
	for _, f := range res.CustomFormats {
		id := f.ID
		if id == "" {
			id = f.Name
		}
		score := f.Scores["default"]
		if v, ok := res.CustomFormatScores[id]; ok {
			score = v
		}
		if v, ok := p.CustomFormatScores[id]; ok {
			score = v
		}
		if score != 0 {
			bundle[id] = score
		}
	}
	width := spread(bundle)
	for k, v := range bundle {
		conv.scores[k] = v
	}
	for i := len(levels) - 1; i >= 0; i-- {
		w := roundUpNice(width + 1)
		for id, u := range levels[i].units {
			conv.scores[id] += u * w
		}
		width += levels[i].span * w
	}
	for k, v := range conv.scores {
		if v == 0 {
			delete(conv.scores, k)
		}
	}
	conv.min = p.MinCustomFormatScore
	if conv.min == nil {
		conv.min = res.MinCustomFormatScore
	}
	return conv
}

// spread bounds how far apart two releases' bundle scores can be: a family
// contributes one of its formats, every other format independently.
func spread(scores map[string]int) int {
	fam := map[string][2]int{} // family → min, max
	total := 0
	for _, f := range core.BuiltinFormats() {
		v, ok := scores[f.ID]
		if !ok || f.Family == "" {
			continue
		}
		mm := fam[f.Family]
		mm[0], mm[1] = min(mm[0], v), max(mm[1], v)
		fam[f.Family] = mm
	}
	for _, mm := range fam {
		total += mm[1] - mm[0]
	}
	families := map[string]string{}
	for _, f := range core.BuiltinFormats() {
		families[f.ID] = f.Family
	}
	for id, v := range scores {
		if families[id] == "" {
			total += abs(v)
		}
	}
	return total
}

// roundUpNice is the smallest 1, 2 or 5 × 10ⁿ that is at least x.
func roundUpNice(x int) int {
	for p := 1; ; p *= 10 {
		for _, m := range []int{1, 2, 5} {
			if m*p >= x {
				return m * p
			}
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// ConvertedOrderGroup names the one quality group of a profile converted
// from an order that ranked something above resolution
// (core.QualityProfile.ConvertedRanking). The console shows it translated.
const ConvertedOrderGroup = "All resolutions (from your old order)"

// Quality order shapes a conversion produces.
const (
	orderByResolution = iota // one group per resolution, highest first
	orderBySource            // each preferred source's resolutions, then a group per resolution
	orderFlat                // one group of every quality
)

// qualityOrder builds a converted profile's quality order (see the order
// shapes above) over the resolutions in the profile's range.
func qualityOrder(p legacyProfile, preferred []string, mode int) []core.QualityItem {
	var heights []int
	for _, h := range core.FormatHeights {
		if (p.MaxHeight == 0 || h <= p.MaxHeight) && h >= p.MinHeight {
			heights = append(heights, h)
		}
	}
	if len(heights) == 0 {
		heights = []int{core.FormatHeights[len(core.FormatHeights)-1]}
	}
	all := slices.Clone(RankingSources)
	if p.AllowCam {
		all = append(all, string(core.SourceCam), string(core.SourceScreener))
	}
	if mode == orderFlat {
		// Every height, not only the profile's range: resolution formats
		// rank them, and a starved encode graded below the range must still
		// rank by its other formats, as it did.
		item := core.QualityItem{Name: ConvertedOrderGroup}
		for _, h := range core.FormatHeights {
			if p.MaxHeight > 0 && h > p.MaxHeight {
				continue
			}
			for _, s := range all {
				item.Qualities = append(item.Qualities, fmt.Sprintf("%s-%d", s, h))
			}
		}
		return []core.QualityItem{item}
	}
	var out []core.QualityItem
	others := all
	if mode == orderBySource {
		for _, src := range preferred {
			for _, h := range heights {
				out = append(out, core.QualityItem{Qualities: []string{fmt.Sprintf("%s-%d", src, h)}})
			}
		}
		others = nil
		for _, s := range all {
			if !slices.Contains(preferred, s) {
				others = append(others, s)
			}
		}
	}
	for _, h := range heights {
		item := core.QualityItem{Name: fmt.Sprintf("%dp", h)}
		for _, s := range others {
			item.Qualities = append(item.Qualities, fmt.Sprintf("%s-%d", s, h))
		}
		if len(item.Qualities) > 0 {
			out = append(out, item)
		}
	}
	return out
}

// stepName is an old ladder step as the console named it.
func stepName(step string) string {
	if strings.HasPrefix(step, "rule:") {
		return "a Prefer rule"
	}
	return map[string]string{stepResolution: "Resolution", stepReleaseType: "Release type", stepHDR: "HDR",
		stepCustomScore: "Custom score", stepVideoCodec: "Video codec", stepAudioFormat: "Audio format", stepSize: "Larger file"}[step]
}

// ResolutionGroups is the quality order of Ether's own profiles: one group
// per resolution from max down to min (0 for no limit), every release type
// in each, CAM and screener copies included when allowCam is set.
func ResolutionGroups(maxHeight, minHeight int, allowCam bool) []core.QualityItem {
	return qualityOrder(legacyProfile{MaxHeight: maxHeight, MinHeight: minHeight, AllowCam: allowCam}, nil, orderByResolution)
}

func knownSources(list []core.Source) []string {
	var out []string
	for _, s := range list {
		if slices.Contains(core.FormatSources, s) && !slices.Contains(out, string(s)) {
			out = append(out, string(s))
		}
	}
	return out
}

func knownCodecs(list []string) []string {
	var out []string
	for _, c := range list {
		if slices.Contains(core.FormatCodecs, c) {
			out = append(out, c)
		}
	}
	return out
}

func knownAudio(list []string) []string {
	var out []string
	for _, a := range list {
		if slices.Contains(core.FormatAudioCodecs, a) {
			out = append(out, a)
		}
	}
	return out
}
