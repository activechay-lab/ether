package core

import "context"

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
	return context.WithValue(ctx, catalogViewerKey{}, u.ID)
}
func CatalogViewerID(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	id, _ := ctx.Value(catalogViewerKey{}).(int64)
	return id
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
