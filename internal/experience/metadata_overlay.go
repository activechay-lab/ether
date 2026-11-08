package experience

import (
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
