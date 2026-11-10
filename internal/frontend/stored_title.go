package frontend

import (
	"context"
	"time"
)

// StoredTitleLookup reads local metadata without remote discovery. Callers
// must apply their own authorization before exposing the returned title.
type StoredTitleLookup interface {
	StoredTitle(context.Context, RatingKey) (*Title, error)
}

// AvailabilitySource is the optional capability reporting whether a title
// is currently available, and whether that answer is known.
type AvailabilitySource interface {
	Available(context.Context, RatingKey) (bool, bool)
}

// SearchCacheRefiller is the optional capability that refills a title's
// search cache in the background; the channel closes when it finishes.
type SearchCacheRefiller interface {
	RefillSearchCache(*Title) <-chan struct{}
}

// CatalogSchedule is the optional capability listing a show's scheduled
// episodes. Catalogue methods remain released-only; this interface never
// authorizes playback.
type CatalogSchedule interface {
	ScheduledEpisodes(context.Context, RatingKey) ([]Title, error)
}

// CachedTitleSource is the optional capability reading titles already in
// the local cache, without discovery.
type CachedTitleSource interface {
	CachedTitles(context.Context, []RatingKey) ([]Title, error)
}

// StoredTitlePreloader warms stored titles into the local title cache in one batch.
type StoredTitlePreloader interface {
	PreloadStored(context.Context, []RatingKey)
}

// CachedTitleReader reads one mirrored title without triggering a provider refresh.
type CachedTitleReader interface {
	CachedGet(context.Context, RatingKey) (*Title, error)
}

// CachedTitleBatchReader reads mirrored titles in bulk without provider refresh.
type CachedTitleBatchReader interface {
	CachedGetMany(context.Context, []RatingKey) (map[RatingKey]Title, error)
}

// ContinueEpisodesCatalog resolves the next continue-watching episode per show in batch.
type ContinueEpisodesCatalog interface {
	ContinueEpisodes(context.Context, []RatingKey) (map[RatingKey]*Title, error)
}

// ArtworkPersistenceCatalog reports whether computed artwork palettes may be persisted for a key.
type ArtworkPersistenceCatalog interface {
	PersistsArtwork(context.Context, RatingKey) bool
}

// RowFactsLoader populates stored row-level facts on a slice of titles.
type RowFactsLoader interface {
	LoadRowFacts(context.Context, []Title) []Title
}

// SearchVisibilityCatalog reports whether a search hit passes catalogue search visibility rules.
type SearchVisibilityCatalog interface {
	SearchVisible(*Title) bool
}

// SearchListDetailsCatalog enriches search results with list-level details in batch.
type SearchListDetailsCatalog interface {
	SearchListDetails(context.Context, []Title) ([]Title, error)
}

// SQLAvailabilityCatalog reports whether browse queries apply availability filtering in SQL.
type SQLAvailabilityCatalog interface {
	BrowseAvailabilityInSQL() bool
}

// SQLPlaylistBrowseCatalog reports whether smart playlist browse queries execute in SQL.
type SQLPlaylistBrowseCatalog interface {
	BrowsePlaylistInSQL() bool
}

// OnDeckPageCatalog pages on-deck items directly from the catalogue store.
type OnDeckPageCatalog interface {
	OnDeckPageSince(context.Context, int64, int, int, Filter, bool, time.Time) (WatchPage, error)
}

// RecentPageCatalog pages recently added items directly from the catalogue store.
type RecentPageCatalog interface {
	RecentPage(context.Context, BrowseQuery, []MediaType) (Page, error)
}

// UndatedSearchHitCatalog reports whether a key corresponds to an undated search hit.
type UndatedSearchHitCatalog interface {
	UndatedSearchHit(context.Context, RatingKey) bool
}

// TitleOpenabilityCatalog reports whether a title's stored facts allow opening it.
type TitleOpenabilityCatalog interface {
	TitleOpenable(*Title) bool
}

// VisibilityRowsCatalog reads mirrored visibility rows with requested facts in one batch.
type VisibilityRowsCatalog interface {
	CachedVisibilityRows(context.Context, []RatingKey, TitleFacts) (map[RatingKey]Title, error)
}

// ShowParentResolver resolves parent show titles for episodes/seasons in batch.
type ShowParentResolver interface {
	ResolveShowParents(context.Context, []RatingKey) (map[RatingKey]Title, error)
}

// SupportVisibilityCatalog reads a title and its visibility diagnostic state for support capture.
type SupportVisibilityCatalog interface {
	SupportVisibility(context.Context, RatingKey) (*Title, string, error)
}

// HomeRowLayoutGate reports whether a home row is enabled for a viewer's layout.
type HomeRowLayoutGate interface {
	HomeRowEnabled(context.Context, *User, HubContext, string) bool
}

// PlaylistHeaderLister lists playlist headers without loading item membership.
type PlaylistHeaderLister interface {
	ListHeaders(context.Context, int64) ([]Playlist, error)
}
