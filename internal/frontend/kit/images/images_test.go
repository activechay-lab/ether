package images

import (
	"image"
	"image/color"
	"testing"
)

func TestImages(t *testing.T) {
	first, again := Tag([]byte("a")), Tag([]byte("a"))
	if first == Tag([]byte("b")) || first != again {
		t.Fatal("tag")
	}
	src := image.NewNRGBA(image.Rect(3, 4, 5, 5))
	src.SetNRGBA(3, 4, color.NRGBA{255, 0, 0, 255})
	src.SetNRGBA(4, 4, color.NRGBA{0, 0, 255, 255})
	dst, err := Resize(src, 4, 1)
	if err != nil || dst.NRGBAAt(0, 0).R != 255 || dst.NRGBAAt(3, 0).B != 255 {
		t.Fatal(dst, err)
	}
	blur, err := Blur(src)
	if err != nil || blur.NRGBAAt(0, 0).B == 0 || blur.NRGBAAt(1, 0).R == 0 {
		t.Fatal(blur, err)
	}
	if _, err := Resize(src, 0, 1); err == nil {
		t.Fatal("accepted dimensions")
	}
}
