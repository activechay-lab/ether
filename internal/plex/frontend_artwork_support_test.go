package plex

import (
	artworkservice "github.com/braedonsaunders/ether/internal/artwork/service"

	"github.com/braedonsaunders/ether/internal/frontend"

	"github.com/braedonsaunders/ether/internal/plex/model"
)

func renderUltraBlur(c model.UltraBlurColors, tr frontend.ArtworkTransform) ([]byte, string, error) {
	return artworkservice.RenderUltraBlur(artworkservice.Palette{TopLeft: c.TopLeft, TopRight: c.TopRight, BottomLeft: c.BottomLeft, BottomRight: c.BottomRight}, tr)
}
