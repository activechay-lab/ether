//go:build goexperiment.simd && (amd64 || arm64)

// Package rapidyenc contains the vendored pure-Go SIMD line kernels. Ether
// retains framing, bounds enforcement, scalar tails and CRC verification.
package rapidyenc

// Decode decodes a whole-vector prefix, leaving the tail to Ether's SWAR
// decoder. dst must have len(src) bytes. Exact and forward in-place decoding
// are supported. No loads or stores extend beyond those slice lengths.
func Decode(dst, src []byte, escaped bool) (consumed, produced int, nextEscaped bool) {
	if !Available() || len(src) < 64 {
		return 0, 0, escaped
	}
	_ = dst[len(src)-1]
	var esc uint64
	if escaped {
		esc = 1
	}
	consumed, produced, esc = decodeVector(dst, src, esc)
	return consumed, produced, esc != 0
}
