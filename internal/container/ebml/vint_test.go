package ebml

import "testing"

func TestVintBoundaries(t *testing.T) {
	for width := 1; width <= 8; width++ {
		b := make([]byte, width)
		b[0] = byte(1 << (8 - width))
		if v, n := Vint(b, false); v != 0 || n != width {
			t.Fatalf("width %d: %d %d", width, v, n)
		}
		b[0] = byte(0xff >> (width - 1))
		for i := 1; i < width; i++ {
			b[i] = 0xff
		}
		v, n := Vint(b, false)
		if !Unknown(v, n) {
			t.Fatalf("unknown width %d: %d %d", width, v, n)
		}
		if _, n := Vint(b[:width-1], false); n != 0 {
			t.Fatalf("accepted short width %d", width)
		}
	}
	if _, n := Vint([]byte{0}, false); n != 0 {
		t.Fatal("accepted zero marker")
	}
}
