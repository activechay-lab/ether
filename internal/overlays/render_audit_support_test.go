package overlays

import (
	"image"
)

// paintBadges is the standalone offline helper; production reuses the parsed font.
func paintBadges(dst *image.RGBA, labels []string, cfg Settings) error {
	return New(nil, Options{}).paintBadges(dst, labels, cfg)
}
