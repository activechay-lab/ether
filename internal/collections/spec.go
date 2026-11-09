package collections

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Field, op, sort and source vocabularies.
const (
	MediaMovie = "movie"
	MediaShow  = "show"
	MediaMixed = "mixed"

	MatchAll = "all"
	MatchAny = "any"

	RefreshLive   = "live"
	RefreshHourly = "hourly"
	RefreshDaily  = "daily"
	RefreshWeekly = "weekly"
)

var (
	tmdbLists = map[string]bool{
		"trending_day": true, "trending_week": true, "popular": true, "top_rated": true,
		"now_playing": true, "upcoming": true, "airing_today": true, "on_the_air": true,
	}
	sortModes = map[string]bool{
		"popularity": true, "rating": true, "released": true, "title": true, "added": true,
		"random": true, "personal": true, "source": true, "latestEpisode": true, "latestSeasonPremiere": true,
	}
	artworkModes = map[string]bool{"auto": true, "collage": true, "backdrop": true, "tmdb": true, "custom": true}
	deviceNames  = map[string]bool{core.DeviceTV: true, core.DeviceMobile: true, core.DeviceWeb: true, core.DeviceDesktop: true}
	personRoles  = map[string]bool{"": true, "cast": true, "director": true, "writer": true, "crew": true}
)

// fieldClass groups rule fields by how they are evaluated.
type fieldClass int

const (
	classTags   fieldClass = iota + 1 // multi-valued names: genre, country, language, contentRating
	classRefs                         // TMDB entities by id or name: keyword, person, studio, network, collection, watchProvider
	classNumber                       // year, decade, rating, votes, popularity, runtime
	classDate                         // released, addedAt, latestEpisode, latestSeasonPremiere
	classBool                         // watched, inProgress (per user)
)

var ruleFields = map[string]fieldClass{
	"genre": classTags, "country": classTags, "language": classTags, "contentRating": classTags,
	"keyword": classRefs, "person": classRefs, "studio": classRefs, "network": classRefs,
	"collection": classRefs, "watchProvider": classRefs,
	"year": classNumber, "decade": classNumber, "rating": classNumber, "votes": classNumber,
	"popularity": classNumber, "runtime": classNumber,
	"released": classDate, "addedAt": classDate, "latestEpisode": classDate, "latestSeasonPremiere": classDate,
	"watched": classBool, "inProgress": classBool,
}

var classOps = map[fieldClass]map[string]bool{
	classTags:   {"is": true, "isNot": true, "in": true, "notIn": true},
	classRefs:   {"is": true, "isNot": true, "in": true, "notIn": true},
	classNumber: {"is": true, "isNot": true, "in": true, "notIn": true, "gte": true, "lte": true, "between": true},
	classDate:   {"gte": true, "lte": true, "between": true, "within": true},
	classBool:   {"is": true, "isNot": true},
}

// ValidationError lists the problems with a spec, keyed by field path
// ("name", "sources[1].url", "rules[0].value"). The admin API renders it
// as a 400 with per-field messages.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+e.Fields[k])
	}
	return "invalid collection: " + strings.Join(parts, "; ")
}

// InvalidFields exposes the field errors without importing this package.
func (e *ValidationError) InvalidFields() map[string]string { return e.Fields }

func (e *ValidationError) add(field, msg string) {
	if e.Fields == nil {
		e.Fields = map[string]string{}
	}
	if _, ok := e.Fields[field]; !ok {
		e.Fields[field] = msg
	}
}

var accentRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Normalize fills defaults in place: an empty source list means the whole
// catalogue, empty match means "all", and so on. It never fails.
func Normalize(s *core.CollectionSpec) {
	s.Name = strings.TrimSpace(s.Name)
	s.Description = strings.TrimSpace(s.Description)
	if s.MediaType == "" {
		s.MediaType = MediaMovie
	}
	if s.MediaType == "tv" || s.MediaType == "series" {
		s.MediaType = MediaShow
	}
	if len(s.Sources) == 0 {
		s.Sources = []core.CollectionSource{{Kind: "catalog"}}
	}
	for i := range s.Sources {
		src := &s.Sources[i]
		src.URL = strings.TrimSpace(src.URL)
		src.List = strings.TrimSpace(src.List)
		src.Label = strings.TrimSpace(src.Label)
		src.Region = strings.ToUpper(strings.TrimSpace(src.Region))
		if src.MediaType == "tv" || src.MediaType == "series" {
			src.MediaType = MediaShow
		}
	}
	if s.Match == "" {
		s.Match = MatchAll
	}
	if s.Rules == nil {
		s.Rules = []core.CollectionRule{}
	}
	if s.Exclude == nil {
		s.Exclude = []core.CollectionRule{}
	}
	if s.Sort == "" {
		s.Sort = "popularity"
		s.SortDesc = true
	}
	if s.Refresh == "" {
		s.Refresh = RefreshDaily
	}
	if s.Artwork.Mode == "" {
		s.Artwork.Mode = "auto"
	}
	s.Artwork.Accent = strings.TrimSpace(s.Artwork.Accent)
	if s.Rotate != nil && s.Rotate.Size <= 0 {
		s.Rotate = nil
	}
	if s.Rotate != nil && s.Rotate.Every == "" {
		s.Rotate.Every = "daily"
	}
	if !s.Targets.Users.All && s.Targets.Users.IDs == nil {
		// The zero value means nobody was chosen explicitly: everyone.
		s.Targets.Users = core.AllUsers()
	}
	s.Targets.Schedule = NormalizeSchedule(s.Targets.Schedule)
}

// NormalizeSchedule trims a schedule's dates and returns nil for a schedule
// that sets nothing.
func NormalizeSchedule(sc *core.CollectionSchedule) *core.CollectionSchedule {
	if sc == nil {
		return nil
	}
	sc.StartDate, sc.EndDate = strings.TrimSpace(sc.StartDate), strings.TrimSpace(sc.EndDate)
	// Midnight is both 0 and 24; an until of midnight is stored as 24 and a
	// from of midnight as 0. A window that covers the whole day sets nothing.
	if sc.ToHour != nil && *sc.ToHour == 0 {
		h := 24
		sc.ToHour = &h
	}
	if sc.FromHour != nil && *sc.FromHour == 24 {
		h := 0
		sc.FromHour = &h
	}
	if sc.FromHour != nil || sc.ToHour != nil {
		from, to := 0, 24
		if sc.FromHour != nil {
			from = *sc.FromHour
		}
		if sc.ToHour != nil {
			to = *sc.ToHour
		}
		if from == to || from == 0 && to == 24 {
			sc.FromHour, sc.ToHour = nil, nil
		}
	}
	if sc.IsZero() {
		return nil
	}
	return sc
}

// Validate checks a normalized spec. It returns a *ValidationError.
func Validate(s *core.CollectionSpec) error { return validateWith(defaultKinds, s) }

func validateWith(reg *KindRegistry, s *core.CollectionSpec) error {
	ve := &ValidationError{}
	switch s.ReleaseMode {
	case "", "released":
	case "upcoming", "mixed":
		if s.LookaheadDays < 1 || s.LookaheadDays > 366 {
			ve.add("lookaheadDays", "must be between 1 and 366 days")
		}
		if s.LocalOnly || len(s.Sources) != 1 || s.Sources[0].Kind != "catalog" {
			ve.add("sources", "Upcoming collections require the whole catalogue source and TMDB discovery")
		}
		if !s.Targets.CollectionsTab || s.Targets.HomeRow || s.Targets.SectionRow {
			ve.add("targets", "Upcoming collections require the Collections tab and cannot use Home or library rows")
		}
	default:
		ve.add("releaseMode", "unknown release mode")
	}
	if s.LocalOnly {
		for _, src := range s.Sources {
			if src.Kind != "catalog" {
				ve.add("localOnly", "Local collections use only the catalogue source")
			}
		}
		for _, r := range append(append([]core.CollectionRule{}, s.Rules...), s.Exclude...) {
			if r.Field == "watchProvider" {
				ve.add("localOnly", "Streaming-provider rules require remote discovery")
			}
		}
	}
	if s.Name == "" {
		ve.add("name", "a name is required")
	} else if len(s.Name) > 120 {
		ve.add("name", "keep the name under 120 characters")
	}
	if len(s.Description) > 2000 {
		ve.add("description", "keep the description under 2000 characters")
	}
	switch s.MediaType {
	case MediaMovie, MediaShow, MediaMixed:
	default:
		ve.add("mediaType", `must be "movie", "show" or "mixed"`)
	}
	if len(s.Sources) > 25 {
		ve.add("sources", "at most 25 sources")
	}
	for i, src := range s.Sources {
		validateSourceIn(reg, ve, fmt.Sprintf("sources[%d]", i), src)
	}
	if s.Match != MatchAll && s.Match != MatchAny {
		ve.add("match", `must be "all" or "any"`)
	}
	if len(s.Rules)+len(s.Exclude) > 50 {
		ve.add("rules", "at most 50 rules")
	}
	for i, r := range s.Rules {
		if _, err := compileRule(r); err != nil {
			ve.add(fmt.Sprintf("rules[%d]", i), err.Error())
		}
	}
	for i, r := range s.Exclude {
		if _, err := compileRule(r); err != nil {
			ve.add(fmt.Sprintf("exclude[%d]", i), err.Error())
		}
	}
	if !sortModes[s.Sort] {
		ve.add("sort", "unknown sort "+strconv.Quote(s.Sort))
	}
	if s.Limit < 0 || s.Limit > maxMembers {
		ve.add("limit", fmt.Sprintf("must be between 0 (no limit) and %d", maxMembers))
	}
	switch s.Refresh {
	case RefreshLive, RefreshHourly, RefreshDaily, RefreshWeekly:
	default:
		ve.add("refresh", `must be "live", "hourly", "daily" or "weekly"`)
	}
	if s.Refresh == RefreshLive && hasRemoteListSource(*s) {
		ve.add("refresh", "Add-on list sources refresh in the background (hourly, daily or weekly)")
	}
	if s.Rotate != nil {
		if s.Rotate.Size > 500 {
			ve.add("rotate.size", "rotate at most 500 titles")
		}
		if s.Rotate.Every != "daily" && s.Rotate.Every != "weekly" {
			ve.add("rotate.every", `must be "daily" or "weekly"`)
		}
	}
	if !artworkModes[s.Artwork.Mode] {
		ve.add("artwork.mode", "unknown artwork mode "+strconv.Quote(s.Artwork.Mode))
	}
	if s.Artwork.Accent != "" && !accentRe.MatchString(s.Artwork.Accent) {
		ve.add("artwork.accent", `use a hex colour like "#e50914"`)
	}
	if s.Artwork.Mode == "custom" {
		if !validCustomArt(s.Artwork.URL) {
			ve.add("artwork.url", "use an http(s) image URL or a TMDB image path")
		}
	}
	validateSchedule(ve, "targets.schedule.", s.Targets.Schedule)
	if len(ve.Fields) > 0 {
		return ve
	}
	return nil
}

// ValidateSchedule checks a normalized collection schedule. It returns a
// *ValidationError whose fields are relative to the schedule ("days").
func ValidateSchedule(sc *core.CollectionSchedule) error {
	ve := &ValidationError{}
	validateSchedule(ve, "", sc)
	if len(ve.Fields) > 0 {
		return ve
	}
	return nil
}

func validateSchedule(ve *ValidationError, prefix string, sc *core.CollectionSchedule) {
	if sc == nil {
		return
	}
	for _, d := range sc.Days {
		if d < 0 || d > 6 {
			ve.add(prefix+"days", "days are 0 (Sunday) to 6 (Saturday)")
		}
	}
	for name, h := range map[string]*int{"fromHour": sc.FromHour, "toHour": sc.ToHour} {
		if h != nil && (*h < 0 || *h > 24) {
			ve.add(prefix+name, "hours are 0 to 24")
		}
	}
	for _, d := range sc.Devices {
		if !deviceNames[d] {
			ve.add(prefix+"devices", "devices are tv, mobile, web or desktop")
		}
	}
	for _, d := range sc.Dayparts {
		if d != core.DaypartMorning && d != core.DaypartAfternoon && d != core.DaypartEvening && d != core.DaypartLate {
			ve.add(prefix+"dayparts", "dayparts are morning, afternoon, evening or late")
		}
	}
	for _, m := range sc.Months {
		if m < 1 || m > 12 {
			ve.add(prefix+"months", "months are 1 (January) to 12 (December)")
		}
	}
	for name, d := range map[string]string{"startDate": sc.StartDate, "endDate": sc.EndDate} {
		if d != "" {
			if _, err := time.Parse("2006-01-02", d); err != nil {
				ve.add(prefix+name, "dates are YYYY-MM-DD")
			}
		}
	}
	if sc.StartDate != "" && sc.EndDate != "" && sc.StartDate > sc.EndDate {
		ve.add(prefix+"endDate", "must not be before the start date")
	}
}

func validCustomArt(u string) bool {
	u = strings.TrimSpace(u)
	if u == "" {
		return false
	}
	if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
		return len(u) < 2048
	}
	return ValidTMDBImagePath(u)
}

var tmdbPathRe = regexp.MustCompile(`^/[A-Za-z0-9_\-]{1,128}\.(jpg|jpeg|png|webp|svg)$`)

// ValidTMDBImagePath reports whether p looks like a TMDB image path
// ("/abc123.jpg"). It rejects traversal and anything that is not a single
// file name.
func ValidTMDBImagePath(p string) bool { return tmdbPathRe.MatchString(p) }

func validateSource(ve *ValidationError, path string, src core.CollectionSource) {
	validateSourceIn(defaultKinds, ve, path, src)
}

func validateSourceIn(reg *KindRegistry, ve *ValidationError, path string, src core.CollectionSource) {
	k, ok := reg.Lookup(src.Kind)
	if !ok {
		ve.add(path+".kind", "unknown source "+strconv.Quote(src.Kind))
		return
	}
	k.Validate(ve, path, src)
}

var (
	regionRe        = regexp.MustCompile(`^[A-Z]{2}$`)
	discoverParamRe = regexp.MustCompile(`^[a-z_]+(\.(gte|lte))?$`)
)

// cloneSpec deep-copies a spec through JSON (specs are small).
func cloneSpec(s core.CollectionSpec) core.CollectionSpec {
	b, err := json.Marshal(s)
	if err != nil {
		return s
	}
	var out core.CollectionSpec
	if err := json.Unmarshal(b, &out); err != nil {
		return s
	}
	return out
}
