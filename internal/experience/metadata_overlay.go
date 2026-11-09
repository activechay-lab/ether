package experience

import (
	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plexmeta"
)

func ApplyMetadataDisplay(title *core.Title, overlay frontend.MetadataOverlay) {
	plexmeta.ApplyDisplay(title, overlay)
}
func SaferMetadataRating(catalogue, overlay string) string {
	return plexmeta.SaferRating(catalogue, overlay)
}

// MetadataOverlayRatingAllowed reports whether a cached metadata overlay's
// rating stays within viewer's content-rating cap when combined with the
// catalogue rating.
func MetadataOverlayRatingAllowed(viewer *core.User, catalogue, overlay string) bool {
	if viewer == nil || viewer.MaxContentRating == "" || overlay == "" {
		return true
	}
	return !contentrating.Exceeds(SaferMetadataRating(catalogue, overlay), viewer.MaxContentRating)
}
