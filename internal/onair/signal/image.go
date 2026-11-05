package signal

import (
	"image"
	"image/color"
)

// ImageAt is the random-access scene/master adapter for the live phosphor engine.
// Replay only the small temporal field; output sampling happens once. Live playout
// keeps its Engine instead, so its tick cost is independent of time on air.
func ImageAt(w, h int, seed, tick int64) *image.RGBA {
	ew, eh := (w+1)&^1, (h+1)&^1
	e := NewStatic(ew, eh, seed)
	for n := int64(0); n <= tick; n++ {
		e.warm.Frame(e.source, n)
	}
	video := make([]byte, ew*eh*3/2)
	e.upscale(video)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y/2*(ew/2) + x/2
			yy := int(video[y*ew+x]) - 16
			u := int(video[ew*eh+i]) - 128
			v := int(video[ew*eh*5/4+i]) - 128
			q := func(n int) byte { return byte(max(0, min(255, (n+128)>>8))) }
			dst.SetRGBA(x, y, color.RGBA{q(298*yy + 459*v), q(298*yy - 55*u - 136*v), q(298*yy + 541*u), 255})
		}
	}
	return dst
}
