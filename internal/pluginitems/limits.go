package pluginitems

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// Store and item validation caps (spec §2.4, §3.2, §3.3, §3.5, §10).
const (
	DefaultMaxPluginItems = 2_000_000
	MaxTitleRunes         = 500
	MaxSummaryRunes       = 20_000
	MaxFieldRunes         = 4_000
	MaxArtRoles           = plugins.MaxKindArtRoles
	MaxHintsBytes         = plugins.MaxKindHintsBytes
	MaxFTSTextBytes       = 8 << 10
	DefaultBrowseLimit    = 200
	MaxBrowseLimit        = 1000
	MaxBrowseBodyBytes    = 8 << 20
	MaxSyncConcurrency    = 4
	DeleteGuardRatio      = 0.5
)

var (
	// ErrMaxItemsExceeded is returned when an upsert would push a plugin
	// past its total item cap across kinds (spec §2.4).
	ErrMaxItemsExceeded = fmt.Errorf("%w: plugin item cap exceeded", plugins.ErrQuotaExceeded)
	// ErrInvalidItem is returned when an item fails envelope or field validation.
	ErrInvalidItem = errors.New("pluginitems: invalid item")
	// ErrDeleteGuard is returned when a sync walk or changes batch would
	// delete more than 50% of a library's items (spec §3.5).
	ErrDeleteGuard = errors.New("pluginitems: sync aborted by 50% delete guard")
)

// IndexedFieldValue is one typed EAV row for plugin_item_fields (spec §2.3).
type IndexedFieldValue struct {
	Field string
	Text  string
	Int   int64
	Real  float64
}

// ValidatedItem holds a cleaned KindItem plus its extracted indexed field rows
// and FTS search text.
type ValidatedItem struct {
	Item          core.KindItem
	IndexedFields []IndexedFieldValue
	SearchFields  string
	DroppedFields int
}

// ValidateItem validates one KindItem against its MediaKindMeta declaration
// (spec §3.3): level and optional parentLevel, field types and caps, string
// lengths, art roles and URLs, hints JSON object cap, and SortTitle derivation.
// Unknown field keys are dropped and counted in DroppedFields; wrong field
// types or violated caps reject the item with an error wrapping ErrInvalidItem.
func ValidateItem(decl plugins.MediaKindMeta, it core.KindItem, parentLevel string) (ValidatedItem, error) {
	if msg := plugins.CheckKindItem(decl, it); msg != "" {
		return ValidatedItem{}, fmt.Errorf("%w: item %q: %s", ErrInvalidItem, it.ID, msg)
	}
	lv, at, ok := decl.Level(it.Level)
	if !ok {
		return ValidatedItem{}, fmt.Errorf("%w: item %q: undeclared level %q", ErrInvalidItem, it.ID, it.Level)
	}
	if at > 0 && parentLevel != "" && parentLevel != decl.Levels[at-1].Key {
		return ValidatedItem{}, fmt.Errorf("%w: item %q: level %q requires parent level %q, got %q",
			ErrInvalidItem, it.ID, it.Level, decl.Levels[at-1].Key, parentLevel)
	}
	if !lv.Playable {
		it.Playable = false
	}

	declaredArt := make(map[string]bool, len(decl.Art)+len(plugins.BuiltinKindArtRoles))
	for r := range plugins.BuiltinKindArtRoles {
		declaredArt[r] = true
	}
	for _, a := range decl.Art {
		declaredArt[a.Role] = true
	}
	for _, a := range it.Art {
		if !declaredArt[a.Role] {
			return ValidatedItem{}, fmt.Errorf("%w: item %q: undeclared art role %q", ErrInvalidItem, it.ID, a.Role)
		}
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.URL)), "blob:") {
			return ValidatedItem{}, fmt.Errorf("%w: item %q: blob: art refs are only allowed from resolver/container paths", ErrInvalidItem, it.ID)
		}
	}

	if strings.TrimSpace(it.SortTitle) == "" {
		it.SortTitle = SortTitle(it.Title)
	} else {
		it.SortTitle = FoldSortTitle(it.SortTitle)
	}
	if it.SortTitle == "" {
		it.SortTitle = strings.ToLower(strings.TrimSpace(it.Title))
	}

	if len(it.Hints) == 0 {
		it.Hints = json.RawMessage("{}")
	}

	declaredFields := make(map[string]plugins.KindField, len(decl.Fields))
	for _, f := range decl.Fields {
		declaredFields[f.Key] = f
	}
	searchFieldSet := make(map[string]bool, len(decl.Browse.SearchFields))
	for _, sf := range decl.Browse.SearchFields {
		searchFieldSet[sf] = true
	}

	var (
		cleanedFields map[string]json.RawMessage
		indexed       []IndexedFieldValue
		searchParts   []string
		dropped       int
	)
	if len(it.Fields) > 0 {
		cleanedFields = make(map[string]json.RawMessage, len(it.Fields))
		for k, raw := range it.Fields {
			fdecl, exists := declaredFields[k]
			if !exists {
				dropped++
				continue
			}
			vals, texts, err := validateFieldValue(fdecl, raw)
			if err != nil {
				return ValidatedItem{}, fmt.Errorf("%w: item %q field %q: %v", ErrInvalidItem, it.ID, k, err)
			}
			cleanedFields[k] = raw
			if fdecl.Indexed {
				indexed = append(indexed, vals...)
			}
			if searchFieldSet[k] && len(texts) > 0 {
				searchParts = append(searchParts, strings.Join(texts, " "))
			}
		}
	}
	it.Fields = cleanedFields

	searchText := strings.Join(searchParts, " ")
	if len(searchText) > MaxFTSTextBytes {
		searchText = clipUTF8(searchText, MaxFTSTextBytes)
	}

	return ValidatedItem{
		Item:          it,
		IndexedFields: indexed,
		SearchFields:  searchText,
		DroppedFields: dropped,
	}, nil
}

func validateFieldValue(f plugins.KindField, raw json.RawMessage) ([]IndexedFieldValue, []string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil, errors.New("field value cannot be null")
	}
	switch f.Type {
	case "string":
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, nil, errors.New("must be a string")
		}
		if utf8.RuneCountInString(s) > MaxFieldRunes {
			return nil, nil, fmt.Errorf("string exceeds %d runes", MaxFieldRunes)
		}
		return []IndexedFieldValue{{Field: f.Key, Text: s}}, []string{s}, nil

	case "enum":
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, nil, errors.New("must be an enum string")
		}
		if utf8.RuneCountInString(s) > MaxFieldRunes {
			return nil, nil, fmt.Errorf("enum exceeds %d runes", MaxFieldRunes)
		}
		allowed := false
		for _, v := range f.Enum {
			if v == s {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, nil, fmt.Errorf("value %q is not in declared enum", s)
		}
		return []IndexedFieldValue{{Field: f.Key, Text: s}}, []string{s}, nil

	case "int", "date", "duration":
		n, err := parseJSONInt64(trimmed)
		if err != nil {
			return nil, nil, fmt.Errorf("must be an integer (%s)", f.Type)
		}
		if f.Type == "duration" && n < 0 {
			return nil, nil, errors.New("duration must not be negative")
		}
		return []IndexedFieldValue{{Field: f.Key, Int: n}}, nil, nil

	case "float":
		var v float64
		if err := json.Unmarshal(trimmed, &v); err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, nil, errors.New("must be a number")
		}
		return []IndexedFieldValue{{Field: f.Key, Real: v}}, nil, nil

	case "bool":
		var b bool
		if err := json.Unmarshal(trimmed, &b); err != nil {
			return nil, nil, errors.New("must be a boolean")
		}
		var iv int64
		txt := "false"
		if b {
			iv = 1
			txt = "true"
		}
		return []IndexedFieldValue{{Field: f.Key, Text: txt, Int: iv}}, nil, nil

	case "strings":
		var arr []string
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return nil, nil, errors.New("must be an array of strings")
		}
		if len(arr) > 256 {
			return nil, nil, errors.New("strings array has more than 256 values")
		}
		seen := make(map[string]bool, len(arr))
		out := make([]IndexedFieldValue, 0, len(arr))
		texts := make([]string, 0, len(arr))
		for _, s := range arr {
			if utf8.RuneCountInString(s) > MaxFieldRunes {
				return nil, nil, fmt.Errorf("string element exceeds %d runes", MaxFieldRunes)
			}
			texts = append(texts, s)
			if !seen[s] {
				seen[s] = true
				out = append(out, IndexedFieldValue{Field: f.Key, Text: s})
			}
		}
		return out, texts, nil

	case "people":
		var strArr []string
		if err := json.Unmarshal(trimmed, &strArr); err == nil {
			if len(strArr) > 256 {
				return nil, nil, errors.New("people array has more than 256 entries")
			}
			seen := make(map[string]bool, len(strArr))
			out := make([]IndexedFieldValue, 0, len(strArr))
			texts := make([]string, 0, len(strArr))
			for _, name := range strArr {
				if utf8.RuneCountInString(name) > MaxFieldRunes || strings.TrimSpace(name) == "" {
					return nil, nil, errors.New("person name must be non-empty and within 4000 runes")
				}
				texts = append(texts, name)
				if !seen[name] {
					seen[name] = true
					out = append(out, IndexedFieldValue{Field: f.Key, Text: name})
				}
			}
			return out, texts, nil
		}
		var objArr []struct {
			Name string `json:"name"`
			Role string `json:"role,omitempty"`
		}
		if err := json.Unmarshal(trimmed, &objArr); err != nil {
			return nil, nil, errors.New("must be an array of person names or {name, role} objects")
		}
		if len(objArr) > 256 {
			return nil, nil, errors.New("people array has more than 256 entries")
		}
		seen := make(map[string]bool, len(objArr))
		out := make([]IndexedFieldValue, 0, len(objArr))
		texts := make([]string, 0, len(objArr))
		for _, p := range objArr {
			if strings.TrimSpace(p.Name) == "" || utf8.RuneCountInString(p.Name) > MaxFieldRunes || utf8.RuneCountInString(p.Role) > MaxFieldRunes {
				return nil, nil, errors.New("person name must be non-empty and within 4000 runes")
			}
			texts = append(texts, p.Name)
			if !seen[p.Name] {
				seen[p.Name] = true
				out = append(out, IndexedFieldValue{Field: f.Key, Text: p.Name})
			}
		}
		return out, texts, nil

	default:
		return nil, nil, fmt.Errorf("unsupported field type %q", f.Type)
	}
}

func parseJSONInt64(raw []byte) (int64, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var num json.Number
	if err := dec.Decode(&num); err != nil {
		return 0, err
	}
	if dec.More() {
		return 0, errors.New("trailing data")
	}
	if n, err := num.Int64(); err == nil {
		return n, nil
	}
	f, err := num.Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f {
		return 0, errors.New("not an integer")
	}
	if f < float64(math.MinInt64) || f >= float64(math.MaxInt64) {
		return 0, errors.New("integer overflow")
	}
	return int64(f), nil
}

func clipUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	s = s[:maxBytes]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
