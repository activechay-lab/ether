//go:build !cgo && goexperiment.simd

package yenc

import "simd/archsimd"

// This scan only enforces the input/output bounds. Payload decoding and CRC
// calculation are performed by the upstream RapidYenc public decoder.
func rapidLineInfo(line []byte, escaped bool) (int, bool, bool) {
	if escaped {
		n, next := decodedLineSize(line, true)
		return n, next, true
	}
	eqByte := archsimd.BroadcastUint8x16('=')
	cr := archsimd.BroadcastUint8x16('\r')
	lf := archsimd.BroadcastUint8x16('\n')
	var previous, adjacent, invalid archsimd.Uint8x16
	count, i := 0, 0
	for ; i+16 <= len(line); i += 16 {
		v := archsimd.LoadUint8x16(line[i:])
		eq := v.Equal(eqByte).ToInt8x16().ToBits()
		adjacent = adjacent.Or(eq.And(eq.ConcatShiftBytesRight(previous, 15)))
		invalid = invalid.Or(v.Equal(cr).Or(v.Equal(lf)).ToInt8x16().ToBits())
		count += int(uint8(0 - eq.ReduceSum()))
		previous = eq
	}
	if adjacent.ReduceMax() != 0 {
		n, next := decodedLineSize(line, false)
		return n, next, true
	}
	normalize := invalid.ReduceMax() != 0
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
