package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	kindKeyRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	kindFieldRe = regexp.MustCompile(`^[a-z][a-zA-Z0-9_]{0,39}$`)
	kindArtRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

// Envelope keys a declared field may not shadow (spec §2.1).
var kindEnvelopeKeys = map[string]bool{
	"id": true, "title": true, "sortTitle": true, "year": true,
	"released": true, "summary": true, "rating": true, "index": true,
	"parentId": true, "level": true,
}

var kindFieldTypes = map[string]bool{
	"string": true, "int": true, "float": true, "bool": true, "date": true,
	"duration": true, "enum": true, "strings": true, "people": true,
}

var kindSorts = map[string]bool{
	"title": true, "added": true, "released": true, "year": true, "rating": true, "index": true,
}

var mediaBrowserCollectionTypes = map[string]bool{
	"movies": true, "tvshows": true, "music": true, "audiobooks": true,
	"books": true, "photos": true, "homevideos": true,
}

var mediaBrowserItemTypes = map[string]bool{
	"Movie": true, "Series": true, "Season": true, "Episode": true,
	"MusicAlbum": true, "Audio": true, "Book": true, "PhotoAlbum": true, "Photo": true,
}

func validateMediaKinds(m Manifest, add func(string, string), label func(string, string, int)) {
	e := m.Ether
	kinds := e.Contributes.MediaKinds
	hasProvider := m.HasCapability(CapCatalogProvider)
	hasResolver := m.HasCapability(CapKindResolver)
	if e.Kind == KindContent && !hasProvider && !hasResolver {
		add("ether.capabilities", "a content plugin needs catalog_provider or kind_resolver")
	}
	if (hasProvider || hasResolver) && len(kinds) == 0 {
		add("ether.contributes.mediaKinds", "is required for catalog_provider and kind_resolver")
	}
	if !hasProvider && !hasResolver && len(kinds) > 0 {
		add("ether.contributes.mediaKinds", "needs the catalog_provider or kind_resolver capability")
	}
	seen := map[string]bool{}
	for i, k := range kinds {
		base := fmt.Sprintf("ether.contributes.mediaKinds[%d]", i)
		validateMediaKind(m, k, base, hasProvider, add, label)
		if seen[k.Key] {
			add(base+".key", "is listed twice")
		}
		seen[k.Key] = true
	}
}

func validateMediaKind(m Manifest, k MediaKindMeta, base string, hasProvider bool, add func(string, string), label func(string, string, int)) {
	if !kindKeyRe.MatchString(k.Key) {
		add(base+".key", "must be lower case letters, digits and dashes, at most 32 characters")
	}
	label(base+".label", k.Label, 60)
	label(base+".library.name", k.Library.Name, 60)
	if unsafePath(k.Library.Icon) {
		add(base+".library.icon", "must stay inside the package")
	}

	if len(k.Levels) < 1 || len(k.Levels) > 5 {
		add(base+".levels", "must declare one to five levels")
	}
	levelKeys := map[string]bool{}
	playable := false
	for j, lv := range k.Levels {
		lbase := fmt.Sprintf("%s.levels[%d]", base, j)
		if !kindKeyRe.MatchString(lv.Key) {
			add(lbase+".key", "must be lower case letters, digits and dashes, at most 32 characters")
		}
		if levelKeys[lv.Key] {
			add(lbase+".key", "is listed twice")
		}
		levelKeys[lv.Key] = true
		label(lbase+".label", lv.Label, 40)
		if lv.Playable {
			playable = true
		}
	}
	if len(k.Levels) > 0 && !playable {
		add(base+".levels", "at least one level must be playable")
	}

	fields := map[string]KindField{}
	for j, f := range k.Fields {
		fbase := fmt.Sprintf("%s.fields[%d]", base, j)
		switch {
		case !kindFieldRe.MatchString(f.Key):
			add(fbase+".key", "must start with a lower case letter and use letters, digits and underscores, at most 40 characters")
		case kindEnvelopeKeys[f.Key]:
			add(fbase+".key", "collides with an item envelope key")
		case fields[f.Key].Key != "":
			add(fbase+".key", "is listed twice")
		}
		fields[f.Key] = f
		if !kindFieldTypes[f.Type] {
			add(fbase+".type", "must be string, int, float, bool, date, duration, enum, strings or people")
		}
		label(fbase+".label", f.Label, 40)
		if f.Type == "enum" {
			if len(f.Enum) == 0 {
				add(fbase+".enum", "an enum field needs at least one value")
			}
			seenEnum := map[string]bool{}
			for _, v := range f.Enum {
				if strings.TrimSpace(v) == "" || seenEnum[v] {
					add(fbase+".enum", "values must be non-empty and listed once")
					break
				}
				seenEnum[v] = true
			}
		} else if len(f.Enum) > 0 {
			add(fbase+".enum", "only enum fields list values")
		}
	}

	seenArt := map[string]bool{}
	for j, a := range k.Art {
		abase := fmt.Sprintf("%s.art[%d]", base, j)
		switch {
		case !kindArtRe.MatchString(a.Role):
			add(abase+".role", "must be lower case letters, digits and dashes, at most 32 characters")
		case BuiltinKindArtRoles[a.Role]:
			add(abase+".role", "is a built-in role and needs no declaration")
		case seenArt[a.Role]:
			add(abase+".role", "is listed twice")
		}
		seenArt[a.Role] = true
		label(abase+".label", a.Label, 40)
	}

	validatePresentations(m, k, base+".present", levelKeys, add)

	seenSort := map[string]bool{}
	for j, s := range k.Browse.Sorts {
		if !kindSorts[s] || seenSort[s] {
			add(fmt.Sprintf("%s.browse.sorts[%d]", base, j), "must be title, added, released, year, rating or index, listed once")
		}
		seenSort[s] = true
	}
	for j, f := range k.Browse.Facets {
		field, ok := fields[f]
		if !ok {
			add(fmt.Sprintf("%s.browse.facets[%d]", base, j), "must name a declared field")
		} else if !field.Indexed {
			add(fmt.Sprintf("%s.browse.facets[%d]", base, j), "a facet field must be indexed")
		}
	}
	for j, f := range k.Browse.SearchFields {
		field, ok := fields[f]
		if !ok {
			add(fmt.Sprintf("%s.browse.searchFields[%d]", base, j), "must name a declared field")
		} else if field.Type != "string" && field.Type != "strings" && field.Type != "people" {
			add(fmt.Sprintf("%s.browse.searchFields[%d]", base, j), "a search field must hold text")
		}
	}
	if len(k.Browse.SearchFields) > 0 && !k.Browse.Search {
		add(base+".browse.search", "searchFields need search to be set")
	}
	if k.Browse.LiveSearch && !hasProvider {
		add(base+".browse.liveSearch", "live search needs the catalog_provider capability")
	}

	switch k.Sync.Mode {
	case "":
		if hasProvider {
			add(base+".sync.mode", "a catalog provider kind must be pull or push")
		}
	case KindSyncPull, KindSyncPush:
		if !hasProvider {
			add(base+".sync.mode", "sync needs the catalog_provider capability")
		}
	case "ondemand":
		add(base+".sync.mode", "ondemand is not supported; kinds sync with pull or push")
	default:
		add(base+".sync.mode", "must be pull or push")
	}
	if k.Sync.Refresh != "" {
		if k.Sync.Mode != KindSyncPull {
			add(base+".sync.refresh", "only pull kinds refresh on a schedule")
		} else if d, err := time.ParseDuration(k.Sync.Refresh); err != nil || d < 15*time.Minute {
			add(base+".sync.refresh", "must be a duration of at least 15m")
		}
	}
	if k.Cache.MaxBytes < 0 {
		add(base+".cache.maxBytes", "must not be negative")
	}
	if k.Cache.TTL != "" {
		if d, err := time.ParseDuration(k.Cache.TTL); err != nil || d <= 0 {
			add(base+".cache.ttl", "must be a positive duration")
		}
	}
	if k.Match.ThresholdDefault < 0 || k.Match.ThresholdDefault > 1 {
		add(base+".match.thresholdDefault", "must be between 0 and 1")
	}
}

func validatePresentations(m Manifest, k MediaKindMeta, base string, levelKeys map[string]bool, add func(string, string)) {
	p := k.Present
	protocols := 0
	if p.Plex != nil {
		protocols++
	}
	if p.MediaBrowser != nil {
		protocols++
	}
	if p.Stremio != nil {
		protocols++
	}
	if p.Xtream != nil {
		protocols++
	}
	if p.Silo != nil {
		protocols++
	}
	if p.AdapterOnly {
		if protocols > 0 {
			add(base, "adapterOnly cannot be combined with a protocol presentation")
		}
		if !m.HasCapability(CapFrontendAdapter) {
			add(base+".adapterOnly", "needs a frontend adapter")
		}
		return
	}
	if protocols == 0 {
		add(base, "declare at least one protocol presentation, or adapterOnly with a frontend adapter")
		return
	}
	levels := len(k.Levels)
	if p.Plex != nil {
		switch p.Plex.Base {
		case "movie", "photo":
		case "show", "artist":
			if levels < 2 {
				add(base+".plex.base", p.Plex.Base+" needs at least two levels")
			}
		default:
			add(base+".plex.base", "must be movie, show, artist or photo")
		}
	}
	if p.MediaBrowser != nil {
		if !mediaBrowserCollectionTypes[p.MediaBrowser.CollectionType] {
			add(base+".mediaBrowser.collectionType", "must be movies, tvshows, music, audiobooks, books, photos or homevideos")
		}
		for level, itemType := range p.MediaBrowser.ItemTypes {
			if !levelKeys[level] {
				add(base+".mediaBrowser.itemTypes", "unknown level "+level)
			}
			if !mediaBrowserItemTypes[itemType] {
				add(base+".mediaBrowser.itemTypes", "unknown item type "+itemType)
			}
		}
	}
	if p.Stremio != nil {
		switch p.Stremio.Type {
		case "movie", "channel", "tv":
		case "series":
			if levels < 2 {
				add(base+".stremio.type", "series needs at least two levels")
			}
		default:
			add(base+".stremio.type", "must be movie, series, channel or tv")
		}
	}
	if p.Xtream != nil {
		switch p.Xtream.Category {
		case "movie", "live":
		case "series":
			if levels < 2 {
				add(base+".xtream.category", "series needs at least two levels")
			}
		default:
			add(base+".xtream.category", "must be movie, series or live")
		}
	}
	if p.Silo != nil && !mediaBrowserItemTypes[p.Silo.Type] {
		add(base+".silo.type", "must be a MediaBrowser item type such as Movie or Series")
	}
}

func validateFrontendAdapters(m Manifest, add func(string, string), label func(string, string, int)) {
	adapters := m.Ether.Contributes.FrontendAdapters
	if m.HasCapability(CapFrontendAdapter) && len(adapters) == 0 {
		add("ether.contributes.frontendAdapters", "is required for frontend_adapter")
	}
	if !m.HasCapability(CapFrontendAdapter) && len(adapters) > 0 {
		add("ether.contributes.frontendAdapters", "needs the frontend_adapter capability")
	}
	seen := map[string]bool{}
	for i, a := range adapters {
		base := fmt.Sprintf("ether.contributes.frontendAdapters[%d]", i)
		if !kindKeyRe.MatchString(a.Key) || seen[a.Key] {
			add(base+".key", "must be a unique lower case key, at most 32 characters")
		}
		seen[a.Key] = true
		label(base+".label", a.Label, 60)
		if !map[string]bool{"opds": true, "subsonic": true, "custom": true}[a.ProtocolHint] {
			add(base+".protocolHint", "must be opds, subsonic or custom")
		}
		if !strings.HasPrefix(a.PathPrefix, "/") || a.PathPrefix == "/" {
			add(base+".pathPrefix", "must be a path below the shared listener root, such as /opds")
		}
		if a.DefaultPort < 0 || a.DefaultPort > 65535 {
			add(base+".defaultPort", "must be a port number")
		}
		if a.Auth != "" && !map[string]bool{"ether_token": true, "basic": true, "none_lan": true}[a.Auth] {
			add(base+".auth", "must be ether_token, basic or none_lan")
		}
	}
}

// MediaKindByKey returns the declared kind with this key, if any.
func (m Manifest) MediaKindByKey(key string) (MediaKindMeta, bool) {
	if e := m.EffectiveEther(); e != nil {
		for _, k := range e.Contributes.MediaKinds {
			if k.Key == key {
				return k, true
			}
		}
	}
	return MediaKindMeta{}, false
}

// Level returns the declared level with this key and its position.
func (k MediaKindMeta) Level(key string) (KindLevel, int, bool) {
	for i, lv := range k.Levels {
		if lv.Key == key {
			return lv, i, true
		}
	}
	return KindLevel{}, -1, false
}

// DeclHash is the upgrade gate for an installed kind (spec §2.3/§2.5):
// the SHA-256 of the declaration's canonical JSON. Go marshals struct
// fields in declaration order and map keys sorted, so equal
// declarations always hash equal.
func (k MediaKindMeta) DeclHash() string {
	raw, _ := json.Marshal(k)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ValidateMediaKindsUpgrade applies the additive-only upgrade rule
// (spec §2.5): a new manifest may add kinds, fields, trailing levels
// and art roles, and may change labels, presentations, browse, cache
// and match defaults, but it may not drop or rename a kind, drop or
// retype a field, shrink an enum, drop a field's index, or remove,
// reorder or re-flag a level. A caller that gets an error refuses the
// upgrade unless the owner chose to replace the library contents.
func ValidateMediaKindsUpgrade(prev, next []MediaKindMeta) error {
	var f []FieldError
	add := func(field, msg string) { f = append(f, FieldError{field, msg}) }
	nextByKey := map[string]MediaKindMeta{}
	for _, k := range next {
		nextByKey[k.Key] = k
	}
	for _, old := range prev {
		base := "ether.contributes.mediaKinds." + old.Key
		nk, ok := nextByKey[old.Key]
		if !ok {
			add(base, "kind was removed; removing a kind needs the owner to replace its library")
			continue
		}
		for i, lv := range old.Levels {
			lbase := fmt.Sprintf("%s.levels[%d]", base, i)
			if i >= len(nk.Levels) {
				add(lbase, "level "+lv.Key+" was removed")
				continue
			}
			nl := nk.Levels[i]
			if nl.Key != lv.Key {
				add(lbase+".key", "levels cannot be removed or reordered; add new levels at the end")
			}
			if nl.Playable != lv.Playable || nl.Container != lv.Container {
				add(lbase, "level "+lv.Key+" changed its playable or container flag")
			}
		}
		nextFields := map[string]KindField{}
		for _, nf := range nk.Fields {
			nextFields[nf.Key] = nf
		}
		for _, of := range old.Fields {
			fbase := base + ".fields." + of.Key
			nf, ok := nextFields[of.Key]
			if !ok {
				add(fbase, "field was removed")
				continue
			}
			if nf.Type != of.Type {
				add(fbase+".type", "field changed type from "+of.Type+" to "+nf.Type)
			}
			if of.Indexed && !nf.Indexed {
				add(fbase+".indexed", "field lost its index")
			}
			if of.Type == "enum" && nf.Type == "enum" {
				have := map[string]bool{}
				for _, v := range nf.Enum {
					have[v] = true
				}
				for _, v := range of.Enum {
					if !have[v] {
						add(fbase+".enum", "enum value "+v+" was removed")
					}
				}
			}
		}
	}
	if len(f) > 0 {
		return &ValidationError{f}
	}
	return nil
}
