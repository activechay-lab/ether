package frontend

import "context"

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
