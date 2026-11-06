//go:build !cgo && goexperiment.simd

package yenc

import (
	"math/bits"
	"simd/archsimd"
)

// Called only after RapidYenc's runtime AVX2 availability check succeeds.
// This scans bounds and framing compatibility; RapidYenc decodes the bytes.
func rapidLineInfo(line []byte, escaped bool) (int, bool, bool) {
	if escaped {
		n, next := decodedLineSize(line, true)
		return n, next, true
	}
	eqByte := archsimd.BroadcastUint8x32('=')
	cr := archsimd.BroadcastUint8x32('\r')
	lf := archsimd.BroadcastUint8x32('\n')
	count, i := 0, 0
	var previous uint32
	normalize := false
	for ; i+32 <= len(line); i += 32 {
		v := archsimd.LoadUint8x32(line[i:])
		mask := v.Equal(eqByte).ToBits()
		if mask&((mask<<1)|previous) != 0 {
			n, next := decodedLineSize(line, false)
			return n, next, true
		}
		previous = mask >> 31
		count += bits.OnesCount32(mask)
		normalize = normalize || v.Equal(cr).Or(v.Equal(lf)).ToBits() != 0
	}
	previousEqual := i > 0 && line[i-1] == '='
	for ; i < len(line); i++ {
		c := line[i]
		if c == '=' {
			if previousEqual {
				n, next := decodedLineSize(line, false)
				return n, next, true
			}
			count++
		}
		if c == '\r' || c == '\n' {
			normalize = true
		}
		previousEqual = c == '='
	}
	next := len(line) > 0 && line[len(line)-1] == '='
	return len(line) - count, next, normalize || next
}
