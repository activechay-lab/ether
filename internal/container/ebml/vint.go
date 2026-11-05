// Package ebml provides shared Matroska header primitives.
package ebml

// Width returns a VINT width, or zero for an invalid leading byte.
func Width(first byte) int {
	if first == 0 {
		return 0
	}
	n := 1
	for mask := byte(0x80); first&mask == 0; mask >>= 1 {
		n++
	}
	return n
}

// Vint decodes a variable-width integer. keepMarker retains the ID marker.
// Element ID callers must additionally enforce the four-byte ID limit.
func Vint(b []byte, keepMarker bool) (uint64, int) {
	if len(b) == 0 {
		return 0, 0
	}
	n := Width(b[0])
	if n == 0 || n > len(b) {
		return 0, 0
	}
	v := uint64(b[0])
	if !keepMarker {
		v &= uint64(0xff >> n)
	}
	for _, c := range b[1:n] {
		v = v<<8 | uint64(c)
	}
	return v, n
}

// Unknown reports the all-ones unknown-size encoding.
func Unknown(value uint64, width int) bool {
	return width > 0 && width <= 8 && value == (uint64(1)<<uint(7*width))-1
}
