package pluginitems

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// Project maps a plugin KindItem into a core.Title for legacy Plex/Jellyfin/Emby
// frontends per §6.3.
//
// Mapping rules:
//   - Title.Key = item.Key (a PluginKey(seq))
//   - Title.Type:
//   - Single-level playable kind -> TypeMovie
//   - 2-level hierarchy (e.g. channel -> video) -> root = TypeShow, leaf = TypeEpisode
//   - 3-level hierarchy (e.g. series -> season -> episode) -> TypeShow / TypeSeason / TypeEpisode
//   - Title.Title = item.Title
//   - Title.SortTitle = SortTitle(item.Title) or FoldSortTitle(item.SortTitle)
//   - Title.Summary = item.Summary + formatted declared non-empty fields at the end
//   - Title.Year = item.Year
//   - Title.Released = time.Unix(item.Released, 0)
//   - Title.Runtime = duration field (e.g. "duration" / "runtime") when present
//   - Title.Genres = item.Fields["genre"/"genres"/"tags"] + facet fields
//   - Title.PosterPath = art["poster"] (falling back to art["thumb"])
//   - Title.BackdropPath = art["backdrop"]
//   - Title.StillPath = art["thumb"] (for episode-like items)
//   - Title.LogoPath = art["logo"]
func Project(item core.KindItem, decl plugins.MediaKindMeta, present core.Presentations) core.Title {
	mediaType := projectMediaType(item.Level, decl, present)

	sortTitle := FoldSortTitle(item.SortTitle)
	if sortTitle == "" {
		sortTitle = SortTitle(item.Title)
	}

	t := core.Title{
		Key:        item.Key,
		Type:       mediaType,
		Title:      item.Title,
		SortTitle:  sortTitle,
		Summary:    projectSummary(item, decl),
		Year:       item.Year,
		Rating:     item.Rating,
		ChildCount: item.ChildCount,
		LeafCount:  item.LeafCount,
	}

	if item.Released > 0 {
		t.Released = time.Unix(item.Released, 0).UTC()
		if t.Year == 0 {
			t.Year = t.Released.Year()
		}
	}

	// Hierarchy indices.
	switch mediaType {
	case core.TypeSeason:
		t.Season = item.Index
	case core.TypeEpisode:
		t.Episode = item.Index
		if len(decl.Levels) == 2 {
			t.Season = 1
		}
	}

	// Artwork roles.
	if len(item.Art) > 0 {
		var thumb string
		for _, a := range item.Art {
			u := strings.TrimSpace(a.URL)
			if u == "" {
				continue
			}
			switch a.Role {
			case "poster":
				if t.PosterPath == "" {
					t.PosterPath = u
				}
			case "backdrop":
				if t.BackdropPath == "" {
					t.BackdropPath = u
				}
			case "logo":
				if t.LogoPath == "" {
					t.LogoPath = u
				}
			case "thumb":
				if thumb == "" {
					thumb = u
				}
			}
		}
		if mediaType == core.TypeEpisode {
			t.StillPath = thumb
			if t.PosterPath == "" {
				t.PosterPath = thumb
			}
		} else if t.PosterPath == "" {
			t.PosterPath = thumb
		}
	}

	// Extract runtime from any declared "duration" field or well-known duration keys.
	if sec := extractDurationSeconds(item.Fields, decl); sec > 0 {
		t.Runtime = time.Duration(sec) * time.Second
	}

	// Genres, studios, and people from declared fields.
	t.Genres = extractGenresAndFacets(item, decl)
	if studios := extractStringsFromFields(item.Fields, "studio", "studios", "channel", "publisher", "author"); len(studios) > 0 {
		t.Studios = studios
	}
	if cast := extractPeopleFromFields(item.Fields, "cast", "actors", "narrator", "performers"); len(cast) > 0 {
		t.Cast = cast
	}
	if directors := extractPeopleFromFields(item.Fields, "director", "directors", "creator"); len(directors) > 0 {
		t.Directors = directors
	}
	if writers := extractPeopleFromFields(item.Fields, "writer", "writers"); len(writers) > 0 {
		t.Writers = writers
	}

	return t
}

func projectMediaType(level string, decl plugins.MediaKindMeta, present core.Presentations) core.MediaType {
	if present.MediaBrowser != nil && len(present.MediaBrowser.ItemTypes) > 0 {
		switch present.MediaBrowser.ItemTypes[level] {
		case "Movie":
			return core.TypeMovie
		case "Series":
			return core.TypeShow
		case "Season":
			return core.TypeSeason
		case "Episode":
			return core.TypeEpisode
		}
	}
	if present.Plex != nil && present.Plex.Base == "movie" {
		return core.TypeMovie
	}

	levels := decl.Levels
	n := len(levels)
	if n <= 1 {
		return core.TypeMovie
	}

	idx := -1
	for i, l := range levels {
		if l.Key == level {
			idx = i
			break
		}
	}

	if n == 2 {
		if idx == 0 {
			return core.TypeShow
		}
		return core.TypeEpisode
	}

	switch idx {
	case 0:
		return core.TypeShow
	case n - 1:
		return core.TypeEpisode
	default:
		return core.TypeSeason
	}
}

func projectSummary(item core.KindItem, decl plugins.MediaKindMeta) string {
	base := strings.TrimSpace(item.Summary)
	if len(item.Fields) == 0 || len(decl.Fields) == 0 {
		return base
	}

	var extras []string
	for _, fd := range decl.Fields {
		raw, ok := item.Fields[fd.Key]
		if !ok || len(raw) == 0 {
			continue
		}
		val := formatRawField(fd, raw)
		if val == "" {
			continue
		}
		label := strings.TrimSpace(fd.Label)
		if label == "" {
			label = fd.Key
		}
		extras = append(extras, fmt.Sprintf("%s: %s", label, val))
	}

	if len(extras) == 0 {
		return base
	}
	suffix := strings.Join(extras, " • ")
	if base == "" {
		return suffix
	}
	return base + "\n\n" + suffix
}

func formatRawField(fd plugins.KindField, raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	switch fd.Type {
	case "string", "enum":
		var s string
		if json.Unmarshal(trimmed, &s) == nil {
			return strings.TrimSpace(s)
		}
	case "int", "date", "duration":
		if n, err := parseJSONInt64(trimmed); err == nil {
			return strconv.FormatInt(n, 10)
		}
	case "float":
		var v float64
		if json.Unmarshal(trimmed, &v) == nil {
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
	case "bool":
		var b bool
		if json.Unmarshal(trimmed, &b) == nil {
			if b {
				return "Yes"
			}
			return "No"
		}
	case "strings":
		var arr []string
		if json.Unmarshal(trimmed, &arr) == nil {
			var clean []string
			for _, s := range arr {
				if s = strings.TrimSpace(s); s != "" {
					clean = append(clean, s)
				}
			}
			return strings.Join(clean, ", ")
		}
	case "people":
		var arr []string
		if json.Unmarshal(trimmed, &arr) == nil {
			return strings.Join(arr, ", ")
		}
		var objs []struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(trimmed, &objs) == nil {
			var names []string
			for _, o := range objs {
				if n := strings.TrimSpace(o.Name); n != "" {
					names = append(names, n)
				}
			}
			return strings.Join(names, ", ")
		}
	}
	return ""
}

func extractDurationSeconds(fields map[string]json.RawMessage, decl plugins.MediaKindMeta) int64 {
	if len(fields) == 0 {
		return 0
	}
	for _, fd := range decl.Fields {
		if fd.Type == "duration" {
			if raw, ok := fields[fd.Key]; ok {
				if n, err := parseJSONInt64(raw); err == nil && n > 0 {
					return n
				}
			}
		}
	}
	for _, key := range []string{"duration", "runtime", "length"} {
		if raw, ok := fields[key]; ok {
			if n, err := parseJSONInt64(raw); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

func extractGenresAndFacets(item core.KindItem, decl plugins.MediaKindMeta) []string {
	if len(item.Fields) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		low := strings.ToLower(s)
		if seen[low] {
			return
		}
		seen[low] = true
		out = append(out, s)
	}

	for _, v := range extractStringsFromFields(item.Fields, "genre", "genres", "tags", "tag") {
		add(v)
	}

	facetKeys := append([]string(nil), decl.Browse.Facets...)
	sort.Strings(facetKeys)
	for _, key := range facetKeys {
		for _, v := range extractStringsFromFields(item.Fields, key) {
			add(v)
		}
	}
	return out
}

func extractStringsFromFields(fields map[string]json.RawMessage, keys ...string) []string {
	if len(fields) == 0 {
		return nil
	}
	var out []string
	for _, k := range keys {
		raw, ok := fields[k]
		if !ok || len(raw) == 0 {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
			continue
		}
		var arr []string
		if err := json.Unmarshal(raw, &arr); err == nil {
			for _, elem := range arr {
				if elem = strings.TrimSpace(elem); elem != "" {
					out = append(out, elem)
				}
			}
		}
	}
	return out
}

func extractPeopleFromFields(fields map[string]json.RawMessage, keys ...string) []core.Person {
	if len(fields) == 0 {
		return nil
	}
	var out []core.Person
	for _, k := range keys {
		raw, ok := fields[k]
		if !ok || len(raw) == 0 {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, core.Person{Name: s})
			}
			continue
		}
		var arr []string
		if err := json.Unmarshal(raw, &arr); err == nil {
			for _, name := range arr {
				if name = strings.TrimSpace(name); name != "" {
					out = append(out, core.Person{Name: name})
				}
			}
			continue
		}
		var objs []struct {
			Name string `json:"name"`
			Role string `json:"role,omitempty"`
		}
		if err := json.Unmarshal(raw, &objs); err == nil {
			for _, o := range objs {
				if name := strings.TrimSpace(o.Name); name != "" {
					out = append(out, core.Person{Name: name, Role: strings.TrimSpace(o.Role)})
				}
			}
		}
	}
	return out
}
