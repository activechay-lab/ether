package experience

import "github.com/braedonsaunders/ether/internal/frontend"

// Metadata overlay display/rating helpers live in internal/frontend
// (host_sessions_experience.go); aliases keep host wiring on the same code.
var (
	ApplyMetadataDisplay         = frontend.ApplyMetadataDisplay
	SaferMetadataRating          = frontend.SaferMetadataRating
	MetadataOverlayRatingAllowed = frontend.MetadataOverlayRatingAllowed
)
