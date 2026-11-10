package core

import (
	"encoding/json"
	"strings"
	"unicode"
)

// Plugin kind item envelope and source query types (spec §2.2,
// §3.2, §4.5). These types are the frozen cross-slice contract:
// plugins declare kinds with them, pluginitems stores them, and the
// protocol frontends project them. Keep edits additive.

// ItemRef is a plugin item's stable identity:
// "pk:" + pluginID + ":" + kindKey + ":" + externalID, where the
// external id never contains ':' or '/'. Ether assigns it; plugins
// only ever see their own external ids.
type ItemRef string

// KindItem is the standardised plugin item envelope (spec §3.2).
type KindItem struct {
	Ref        ItemRef                    `json:"-"` // assigned by Ether, never sent to the plugin
	ID         string                     `json:"id"` // externalID
	Level      string                     `json:"level"`
	ParentID   string                     `json:"parentId,omitempty"`
	Index      int                        `json:"index,omitempty"` // position within parent / container leaf index
	Title      string                     `json:"title"`
	SortTitle  string                     `json:"sortTitle,omitempty"` // Ether derives it when empty
	Summary    string                     `json:"summary,omitempty"`
	Year       int                        `json:"year,omitempty"`
	Released   int64                      `json:"released,omitempty"` // unix seconds
	Rating     float64                    `json:"rating,omitempty"`
	Playable   bool                       `json:"playable,omitempty"`
	ChildCount int                        `json:"childCount,omitempty"`
	LeafCount  int                        `json:"leafCount,omitempty"`
	Art        []KindArtRef               `json:"art,omitempty"`
	Fields     map[string]json.RawMessage `json:"fields,omitempty"` // validated against the kind declaration
	Hints      json.RawMessage            `json:"hints,omitempty"`  // opaque; stored and echoed to the resolver verbatim
	ETag       string                     `json:"etag,omitempty"`
}

// KindArtRef is one artwork reference on a KindItem.
type KindArtRef struct {
	Role   string `json:"role"`
	URL    string `json:"url"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// KindPage is one page of kind items.
type KindPage struct {
	Items      []KindItem `json:"items"`
	NextCursor string     `json:"nextCursor,omitempty"` // opaque; absent = last page
	Total      *int       `json:"total,omitempty"`      // provider claim; Ether never trusts it for its own totals
}

// SourceQuery asks Ether to find sources for a kind resolver
// (spec §4.5).
type SourceQuery struct {
	Kind       string   `json:"kind"` // indexer_search|debrid_check|debrid_acquire
	Query      string   `json:"query,omitempty"`
	Category   string   `json:"category,omitempty"`
	InfoHashes []string `json:"infoHashes,omitempty"`
	InfoHash   string   `json:"infoHash,omitempty"`
	Magnet     string   `json:"magnet,omitempty"`
}

// SourceCandidate is one Ether-found release for a resolver to rank,
// carrying the release-hint fields plus the facts matching needs.
type SourceCandidate struct {
	InfoHash  string `json:"infoHash,omitempty"`
	Magnet    string `json:"magnet,omitempty"`
	NZBURL    string `json:"nzbUrl,omitempty"`
	FileIndex int    `json:"fileIndex,omitempty"`
	Title     string `json:"title,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Seeders   int    `json:"seeders,omitempty"`
	Cached    bool   `json:"cached,omitempty"`
}

// PluginLibraryKey is the library key a plugin kind owns
// (spec §1.1): "pk-" + slug(pluginID) + "-" + slug(kindKey), matching
// internal/libraries.UpsertPluginLibrary. The key is an opaque unique
// slug; ownership is always read back from the stored plugin/kind
// columns, never parsed out of the key.
func PluginLibraryKey(pluginID, kindKey string) string {
	return "pk-" + libraryKeySlug(pluginID) + "-" + libraryKeySlug(kindKey)
}

func libraryKeySlug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		default:
			dash = true
		}
	}
	return b.String()
}
