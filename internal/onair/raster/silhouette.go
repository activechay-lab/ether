package raster

import (
	"image"
	"image/color"
)

// Silhouette prepares a monochrome alpha mask once, before rasterization.
// Transparent artwork uses its alpha. Opaque artwork uses luminance, inverted
// for a light backing (sampled from the corner).
func Silhouette(src image.Image) image.Image {
	b := src.Bounds()
	out := image.NewNRGBA(b)
	transparent := false
	for y := b.Min.Y; y < b.Max.Y && !transparent; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := src.At(x, y).RGBA()
			if a < 64250 {
				transparent = true
				break
			}
		}
	}
	corner := color.NRGBAModel.Convert(src.At(b.Min.X, b.Min.Y)).(color.NRGBA)
	invert := int(corner.R)+int(corner.G)+int(corner.B) > 384
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
			a := c.A
			if !transparent {
				l := (2126*int(c.R) + 7152*int(c.G) + 722*int(c.B)) / 10000
				if invert {
					l = 255 - l
				}
				a = byte(l)
			}
			out.SetNRGBA(x, y, color.NRGBA{255, 255, 255, a})
		}
	}
	return out
}
