package raster

import (
	"image"
	"testing"
)

func benchFrame(w, h int) *image.RGBA {
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range im.Pix {
		im.Pix[i] = uint8(i * 31 % 251)
	}
	for i := 3; i < len(im.Pix); i += 4 {
		im.Pix[i] = 200
	}
	return im
}

func BenchmarkBlur1080p(b *testing.B) {
	src := benchFrame(1920, 1080)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blur(src, 24)
	}
}

func BenchmarkBox1080p(b *testing.B) {
	src := benchFrame(1920, 1080)
	dst := image.NewRGBA(src.Bounds())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		box(src, dst, 14, true)
		box(dst, src, 14, false)
	}
}

func BenchmarkComposite1080p(b *testing.B) {
	dst := benchFrame(1920, 1080)
	src := benchFrame(1920, 1080)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		composite(dst, src, 0.85, nil, "normal")
	}
}
