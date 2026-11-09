package core

import (
	"context"
	"sync"
)

// LibraryExclusion is one ordered visibility rule. Slice order is evaluation
// order: the first match wins. Action is "show" or "hide"; an empty Action is
// "hide", so a saved exclusion group stays a hide rule. Conditions inside a
// rule combine with Match "all" or "any". Watch history and metadata are kept.
//
// Scope limits where a hide rule applies. Empty or "everywhere" hides the
// title from browsing and search. "discovery" hides it only from browsing,
// Home and hub rows, recommendations, New Releases, Trending and catalogue
// collections; search, direct lookup and playback still reach it.
type LibraryExclusion struct {
	// Name is optional. Clients label a rule from its conditions.
	Name      string           `json:"name"`
	Action    string           `json:"action,omitempty"` // show, hide; empty means hide
	MediaType string           `json:"mediaType"`        // movie, show, mixed
	Match     string           `json:"match"`            // all, any
	Rules     []CollectionRule `json:"rules"`
	Scope     string           `json:"scope,omitempty"` // everywhere, discovery; empty means everywhere
}

// Visibility rule scopes.
const (
	VisibilityEverywhere = "everywhere"
	VisibilityDiscovery  = "discovery"
)

type explicitSearchKey struct{}

// WithExplicitSearch marks a request as a viewer or owner typing a search.
// Catalogue visibility then applies only rules scoped everywhere, so a title
// hidden from browsing alone can still be found.
func WithExplicitSearch(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, explicitSearchKey{}, true)
}

// ExplicitSearch reports that ctx belongs to an explicit search.
func ExplicitSearch(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(explicitSearchKey{}).(bool)
	return v
}

type searchAffinityKey struct{}

// Search affinity levels: someone on this server has a history with the
// title, or the searching viewer has.
const (
	AffinityServer = 1
	AffinityViewer = 2
)

// SearchAffinityFunc reports the searching viewer's affinity level with
// each of keys (movies and shows) that has one: watched, started or rated,
// a show through any of its episodes.
type SearchAffinityFunc func(ctx context.Context, keys []RatingKey) map[RatingKey]int

// WithSearchAffinity attaches the searching viewer's affinity to ctx, so the
// catalogue ranks titles the viewer knows first among equally good matches.
func WithSearchAffinity(ctx context.Context, fn SearchAffinityFunc) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, searchAffinityKey{}, fn)
}

// SearchAffinity is ctx's viewer affinity, or nil.
func SearchAffinity(ctx context.Context) SearchAffinityFunc {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(searchAffinityKey{}).(SearchAffinityFunc)
	return fn
}

// Catalogue viewer context is set by trusted experience code, never a client filter.
type catalogViewerKey struct{}

func WithCatalogViewer(ctx context.Context, u *User) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if u == nil {
		return ctx
	}
	return context.WithValue(ctx, catalogViewerKey{}, catalogViewer{id: u.ID, restricted: u.MaxContentRating != "" || len(u.AllowedSections) > 0})
}

type catalogViewer struct {
	id         int64
	restricted bool
}

// WithoutCatalogViewer drops the viewer from ctx, for reads that are the same
// for every viewer (a facet name's identifier) and must share one cache entry.
func WithoutCatalogViewer(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithValue(ctx, catalogViewerKey{}, catalogViewer{})
}

func CatalogViewerID(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	v, _ := ctx.Value(catalogViewerKey{}).(catalogViewer)
	return v.id
}

// CatalogViewerRestricted reports a catalogue viewer with a content rating
// cap or section limits: catalogue rules fail closed for them.
func CatalogViewerRestricted(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(catalogViewerKey{}).(catalogViewer)
	return v.restricted
}

// WithCatalogCandidates gathers shared collection membership before viewer rules.
// It is only for background materialization; serving must apply viewer policy.
type catalogCandidatesKey struct{}

func WithCatalogCandidates(ctx context.Context) context.Context {
	return context.WithValue(ctx, catalogCandidatesKey{}, true)
}
func CatalogCandidates(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(catalogCandidatesKey{}).(bool)
	return v
}

type searchListFactsKey struct{}

// WithSearchListFacts marks a search whose results are rendered as list
// items (a Plex search hub: three cast members, the directors, genres,
// countries, studios): the catalogue reads those facts for its page, not
// every credit, keyword and alternative title of every result.
func WithSearchListFacts(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, searchListFactsKey{}, true)
}

// SearchListFacts reports WithSearchListFacts.
func SearchListFacts(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(searchListFactsKey{}).(bool)
	return v
}

type searchSummaryFactsKey struct{}

// WithSearchSummaryFacts marks movie/show searches rendered using scalar title
// metadata and posters only. Visibility and query-filter facets are still read;
// credits and other detail facts are not needed. Partial titles must not enter
// the full-title cache. Searches without this opt-in retain full results.
func WithSearchSummaryFacts(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, searchSummaryFactsKey{}, true)
}

func SearchSummaryFacts(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(searchSummaryFactsKey{}).(bool)
	return v
}

// TitleFacts is a set of the facts a title keeps outside its own row, in
// tables of their own: what a visibility rule, a viewer's policy, the release
// gate or a query filter may read beyond the row.
type TitleFacts uint8

const (
	FactGenres TitleFacts = 1 << iota
	FactStudios
	FactCountries
	FactKeywords
	FactHomeRelease
)

// VisibilityFacts is every fact a visibility decision can read.
const VisibilityFacts = FactGenres | FactStudios | FactCountries | FactKeywords | FactHomeRelease

// SearchCandidates asks a catalogue search for its candidate window, not its
// page: the caller ranks and filters the window itself and reads only the
// page it shows in full. Library movies and shows then come back as their
// title rows with Facts and the facts the catalogue's own policy reads for
// the viewer (browsing and search rules, the release gate). A title Detail
// reports (one the caller judges on its full facts) comes back in full.
// The catalogue records which titles it returned partial; the caller reads
// those of its page in full before showing them. Partial titles never enter
// a cache.
type SearchCandidates struct {
	Facts  TitleFacts
	Detail func(Title) bool

	mu      sync.Mutex
	partial map[RatingKey]bool
}

// MarkPartial records titles a search returned partial.
func (s *SearchCandidates) MarkPartial(keys []RatingKey) {
	if s == nil || len(keys) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.partial == nil {
		s.partial = make(map[RatingKey]bool, len(keys))
	}
	for _, k := range keys {
		s.partial[k] = true
	}
}

// Partial reports a title a search returned partial.
func (s *SearchCandidates) Partial(key RatingKey) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.partial[key]
}

type searchCandidatesKey struct{}

// WithSearchCandidates attaches c to a search (see SearchCandidates). A nil c
// asks for full results again, as for a search ctx hands on to another one.
func WithSearchCandidates(ctx context.Context, c *SearchCandidates) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, searchCandidatesKey{}, c)
}

// SearchCandidatesFrom returns WithSearchCandidates' c, or nil.
func SearchCandidatesFrom(ctx context.Context) *SearchCandidates {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(searchCandidatesKey{}).(*SearchCandidates)
	return c
}
