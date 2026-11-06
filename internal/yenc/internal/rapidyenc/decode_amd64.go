//go:build goexperiment.simd

package rapidyenc

import (
	"math/bits"
	"simd/archsimd"
	"unsafe"
)

// Adapted from mnightingale/rapidyenc 6657b7ca854e (MIT). See LICENSE and README.md.

var compactLUT [32768][16]byte
var available = archsimd.X86.AVX2()

func Available() bool { return available }
func init() {
	if available {
		initAVX2()
	}
}

var (
	broadcastEscapeFirst, broadcastNeg42, broadcastNeg106 archsimd.Int8x32
	broadcastEQ                                           archsimd.Int8x32
	permuteA, permuteB                                    archsimd.Int8x32
	permuteBitMask, broadcastFF                           archsimd.Uint8x32
)

// initAVX2 builds the tables and vector constants used by the AVX2 kernel.
// Only call it when AVX2 is available: constructing the vectors below emits
// VEX-encoded instructions that fault on pre-AVX CPUs.
func initAVX2() {
	const tableSize = 16
	for i := range compactLUT {
		k := i
		p := 0
		for j := range tableSize {
			if (k & 1) == 0 {
				compactLUT[i][p] = byte(j)
				p++
			}
			k >>= 1
		}
		for ; p < tableSize; p++ {
			compactLUT[i][p] = 0x80
		}
	}

	broadcastEscapeFirst = archsimd.LoadInt8x32([]int8{
		-42 - 64, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42,
		-42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42, -42,
	})

	// with permuteBitMask, the reverse of pmovmskb: lane j takes bit j%8 of
	// mask byte j/8
	permuteA = archsimd.LoadInt8x32([]int8{
		0, 0, 0, 0, 0, 0, 0, 0,
		1, 1, 1, 1, 1, 1, 1, 1,
		2, 2, 2, 2, 2, 2, 2, 2,
		3, 3, 3, 3, 3, 3, 3, 3,
	})
	permuteB = archsimd.LoadInt8x32([]int8{
		4, 4, 4, 4, 4, 4, 4, 4,
		5, 5, 5, 5, 5, 5, 5, 5,
		6, 6, 6, 6, 6, 6, 6, 6,
		7, 7, 7, 7, 7, 7, 7, 7,
	})
	permuteBitMask = archsimd.BroadcastUint64x4(0x8040201008040201).AsUint8x32()
	broadcastEQ = archsimd.BroadcastInt8x32('=')
	broadcastNeg42 = archsimd.BroadcastInt8x32(-42)
	broadcastNeg106 = archsimd.BroadcastInt8x32(-42 - 64)
	broadcastFF = archsimd.BroadcastUint8x32(0xff)
}

func decodeVector(dest, src []byte, escFirst uint64) (int, int, uint64) {
	consumed, produced := 0, 0
	var yencOffset archsimd.Int8x32
	if escFirst > 0 {
		yencOffset = broadcastEscapeFirst
	} else {
		yencOffset = broadcastNeg42
	}
	for ; consumed+64 <= len(src); consumed += 64 {
		s := unsafe.Pointer(&src[consumed])
		d := unsafe.Pointer(&dest[produced])
		oDataA := archsimd.LoadUint8x32Array((*[32]uint8)(s)).AsInt8x32()
		oDataB := archsimd.LoadUint8x32Array((*[32]uint8)(unsafe.Add(s, 32))).AsInt8x32()
		cmpEqA := oDataA.Equal(broadcastEQ)
		cmpEqB := oDataB.Equal(broadcastEQ)
		maskEq := uint64(cmpEqB.ToBits())<<32 | uint64(cmpEqA.ToBits())
		mask := maskEq
		if mask == 0 {
			oDataA.Add(yencOffset).AsUint8x32().StoreArray((*[32]uint8)(d))
			oDataB.Add(broadcastNeg42).AsUint8x32().StoreArray((*[32]uint8)(unsafe.Add(d, 32)))
			produced += 64
			escFirst = 0
			yencOffset = broadcastNeg42
		} else {
			maskEqShift1 := (maskEq << 1) + escFirst
			if mask&maskEqShift1 != 0 {
				maskEq = fixEqMask(maskEq, maskEqShift1)
				nextEscFirst := maskEq >> 63
				// next, eliminate anything following a `=` from the special char mask; this eliminates cases of `=\r` so that they aren't removed
				maskEq = (maskEq << 1) | escFirst
				mask &= ^maskEq
				escFirst = nextEscFirst

				// unescape chars following `=`
				{
					// convert maskEq into vector form (i.e. reverse pmovmskb)
					vMaskEqBytes := archsimd.BroadcastUint64x4(maskEq).AsUint8x32()
					vMaskEqA := vMaskEqBytes.PermuteOrZeroGrouped(permuteA).And(permuteBitMask).AsInt8x32().ToMask()
					vMaskEqB := vMaskEqBytes.PermuteOrZeroGrouped(permuteB).And(permuteBitMask).AsInt8x32().ToMask()
					oDataA = oDataA.Add(broadcastNeg106.Merge(yencOffset, vMaskEqA))
					oDataB = oDataB.Add(broadcastNeg106.Merge(broadcastNeg42, vMaskEqB))
				}
			} else {
				escFirst = maskEq >> 63

				{
					vecA := broadcastNeg106.Merge(
						yencOffset,
						cmpEqA.ToInt8x32().AsUint8x32().ConcatShiftBytesRightGrouped(
							cmpEqA.ToInt8x32().ConcatPermute128Scalars(2, 0, broadcastEQ).AsUint8x32(),
							15,
						).Equal(broadcastFF),
					)
					vecB := broadcastNeg106.Merge(
						broadcastNeg42,
						broadcastEQ.Equal(archsimd.LoadUint8x32Array((*[32]uint8)(unsafe.Add(s, 31))).AsInt8x32()),
					)
					oDataA = oDataA.Add(vecA)
					oDataB = oDataB.Add(vecB)
				}
			}

			if escFirst > 0 {
				yencOffset = broadcastEscapeFirst
			} else {
				yencOffset = broadcastNeg42
			}

			{
				// lookup compress masks and shuffle
				oDataA = oDataA.PermuteOrZeroGrouped(new(archsimd.Uint8x32).
					SetLo(archsimd.LoadUint8x16Array(&compactLUT[mask&0x7fff])).
					SetHi(archsimd.LoadUint8x16Array(&compactLUT[(mask>>16)&0x7fff])).
					AsInt8x32())
				// Store lower 128 bits
				oDataA.GetLo().AsUint8x16().StoreArray((*[16]uint8)(d))
				// Store upper 128 bits
				oDataA.GetHi().AsUint8x16().StoreArray((*[16]uint8)(unsafe.Add(d, 16-bits.OnesCount64(mask&0xffff))))

				oDataB = oDataB.PermuteOrZeroGrouped(new(archsimd.Uint8x32).
					SetLo(archsimd.LoadUint8x16Array(&compactLUT[(mask>>32)&0x7fff])).
					SetHi(archsimd.LoadUint8x16Array(&compactLUT[(mask>>48)&0x7fff])).
					AsInt8x32())
				// Store lower 128 bits
				oDataB.GetLo().AsUint8x16().StoreArray((*[16]uint8)(unsafe.Add(d, 32-bits.OnesCount64(mask&0xffffffff))))
				// Store upper 128 bits
				oDataB.GetHi().AsUint8x16().StoreArray((*[16]uint8)(unsafe.Add(d, 48-bits.OnesCount64(mask&0xffffffffffff))))
				produced += 64 - bits.OnesCount64(mask)
			}
		}
	}
	return consumed, produced, escFirst
}
func fixEqMask(mask, maskShift1 uint64) uint64 {
	// isolate the start of each consecutive bit group (e.g. 01011101 -> 01000101)
	start := mask & ^maskShift1

	// this strategy works by firstly separating groups that start on even/odd bits
	// generally, it doesn't matter which one (even/odd) we pick, but clearing even groups specifically allows the escFirst bit in maskShift1 to work
	// (this is because the start of the escFirst group is at index -1, an odd bit, but we can't clear it due to being < 0, so we just retain all odd groups instead)

	even := uint64(0x5555555555555555) // every even bit (01010101...)

	// obtain groups which start on an odd bit (clear groups that start on an even bit, but this leaves an unwanted trailing bit)
	oddGroups := mask + (start & even)

	// clear even bits in odd groups, whilst conversely preserving even bits in even groups
	// the `& mask` also conveniently gets rid of unwanted trailing bits
	return (oddGroups ^ even) & mask
}
