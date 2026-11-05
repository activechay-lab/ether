package plex

import (
	artworkservice "github.com/braedonsaunders/ether/internal/artwork/service"

	"github.com/braedonsaunders/ether/internal/plex/model"
	"image"
)

func ultraBlurFromImage(img image.Image) (model.UltraBlurColors, error) {
	p, err := artworkservice.PaletteFromImage(img)
	return wirePalette(p), err
}
