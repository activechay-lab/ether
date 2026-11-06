//go:build goexperiment.simd

package rapidyenc

// Adapted from mnightingale/rapidyenc 6657b7ca854e (MIT). See LICENSE and README.md.

import (
	"simd/archsimd"
	"unsafe"
)

var (
	compactLUT [32768][16]byte
)

func init() {
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
}

var (
	broadcastEscapeFirst, broadcast42, broadcastNeg106 archsimd.Uint8x16
	broadcastZERO, broadcastEQ, broadcastFF            archsimd.Uint8x16
	permuteBitMask, unescapeLut                        archsimd.Uint8x16
)

func init() {
	broadcastEscapeFirst = archsimd.LoadUint8x16([]byte{
		42 + 64, 42, 42, 42, 42, 42, 42, 42, 42, 42, 42, 42, 42, 42, 42, 42,
	})

	permuteBitMask = archsimd.LoadUint8x16([]byte{1, 2, 4, 8, 16, 32, 64, 128, 1, 2, 4, 8, 16, 32, 64, 128})
	unescapeLut = archsimd.LoadUint8x16([]byte{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1, 1, 1, 1})
	broadcastZERO = archsimd.BroadcastUint8x16(0)
	broadcastEQ = archsimd.BroadcastUint8x16('=')
	broadcast42 = archsimd.BroadcastUint8x16(42)
	broadcastNeg106 = archsimd.BroadcastUint8x16(42 + 64)
	broadcastFF = archsimd.BroadcastUint8x16(0xff)
}

func Available() bool { return true }

func decodeVector(dest, src []byte, escFirst uint64) (int, int, uint64) {
	consumed, produced := 0, 0
	var yencOffset archsimd.Uint8x16
	if escFirst > 0 {
		yencOffset = broadcastEscapeFirst
	} else {
		yencOffset = broadcast42
	}
	for ; consumed+64 <= len(src); consumed += 64 {
		s := unsafe.Pointer(&src[consumed])
		d := unsafe.Pointer(&dest[produced])
		oDataA, oDataB, oDataC, oDataD := vld1q_u8_x4(s)
		cmpEqA := oDataA.Equal(broadcastEQ).ToInt8x16().ToBits()
		cmpEqB := oDataB.Equal(broadcastEQ).ToInt8x16().ToBits()
		cmpEqC := oDataC.Equal(broadcastEQ).ToInt8x16().ToBits()
		cmpEqD := oDataD.Equal(broadcastEQ).ToInt8x16().ToBits()
		if !neonVectIsNonzero(cmpEqA.Or(cmpEqB).Or(cmpEqC).Or(cmpEqD)) {
			oDataA.Sub(yencOffset).StoreArray((*[16]uint8)(d))
			oDataB.Sub(broadcast42).StoreArray((*[16]uint8)(unsafe.Add(d, 16)))
			oDataC.Sub(broadcast42).StoreArray((*[16]uint8)(unsafe.Add(d, 32)))
			oDataD.Sub(broadcast42).StoreArray((*[16]uint8)(unsafe.Add(d, 48)))
			produced += 64
			escFirst = 0
			yencOffset = broadcast42
		} else {
			cmpMerge := addPairs(addPairs(cmpEqA.And(permuteBitMask), cmpEqB.And(permuteBitMask)), addPairs(cmpEqC.And(permuteBitMask), cmpEqD.And(permuteBitMask)))
			cmpCombined := addPairs(cmpMerge, cmpMerge)
			maskEq := cmpCombined.ReshapeToUint64s().GetElem(0)
			mask := maskEq
			// a spec compliant encoder should never generate sequences: ==, =\n and =\r, but we'll handle them to be spec compliant
			// the yEnc specification requires any character following = to be unescaped, not skipped over, so we'll deal with that
			// firstly, check for invalid sequences of = (we assume that these are rare, as a spec compliant yEnc encoder should not generate these)
			maskEqShift1 := (maskEq << 1) | escFirst
			if mask&maskEqShift1 != 0 {
				maskEq = fixEqMask(maskEq, maskEqShift1)
				nextEscFirst := maskEq >> 63
				// next, eliminate anything following a `=` from the special char mask; this eliminates cases of `=\r` so that they aren't removed
				maskEq = (maskEq << 1) | escFirst
				mask &= ^maskEq
				escFirst = nextEscFirst

				// unescape chars following `=`
				{
					maskEqTemp := archsimd.BroadcastUint64x2(maskEq).ReshapeToUint8s()
					cmpCombined = cmpCombined.AndNot(maskEqTemp) // `mask &= ~maskEq` in vector form

					vMaskEqA := maskEqTemp.LookupOrZero(unescapeLut)
					maskEqTemp = maskEqTemp.ConcatShiftBytesRight(maskEqTemp, 2)
					vMaskEqB := maskEqTemp.LookupOrZero(unescapeLut)
					maskEqTemp = maskEqTemp.ConcatShiftBytesRight(maskEqTemp, 2)
					vMaskEqC := maskEqTemp.LookupOrZero(unescapeLut)
					maskEqTemp = maskEqTemp.ConcatShiftBytesRight(maskEqTemp, 2)
					vMaskEqD := maskEqTemp.LookupOrZero(unescapeLut)

					oDataA = oDataA.Sub(broadcastNeg106.IfElse(vtstq_u8(vMaskEqA, permuteBitMask), broadcast42))
					oDataB = oDataB.Sub(broadcastNeg106.IfElse(vtstq_u8(vMaskEqB, permuteBitMask), broadcast42))
					oDataC = oDataC.Sub(broadcastNeg106.IfElse(vtstq_u8(vMaskEqC, permuteBitMask), broadcast42))
					oDataD = oDataD.Sub(broadcastNeg106.IfElse(vtstq_u8(vMaskEqD, permuteBitMask), broadcast42))
				}
			} else {
				// no invalid = sequences found - we can cut out some things from above
				// this code path is a shortened version of above; it's here because it's faster, and what we'll be dealing with most of the time
				escFirst = maskEq >> 63

				oDataA = oDataA.Sub(broadcastNeg106.IfElse(cmpEqA.ConcatShiftBytesRight(broadcast42, 15).Equal(broadcastFF), yencOffset))
				oDataB = oDataB.Sub(broadcastNeg106.IfElse(cmpEqB.ConcatShiftBytesRight(cmpEqA, 15).Equal(broadcastFF), broadcast42))
				oDataC = oDataC.Sub(broadcastNeg106.IfElse(cmpEqC.ConcatShiftBytesRight(cmpEqB, 15).Equal(broadcastFF), broadcast42))
				oDataD = oDataD.Sub(broadcastNeg106.IfElse(cmpEqD.ConcatShiftBytesRight(cmpEqC, 15).Equal(broadcastFF), broadcast42))
			}

			yencOffset = yencOffset.SetElem(0, uint8((escFirst<<6)|42))

			{
				//// all that's left is to 'compress' the data (skip over masked chars)
				counts := 0x0808080808080808 - cmpCombined.OnesCount().ReshapeToUint64s().GetElem(0)
				counts += counts >> 8

				countA := int(counts & 0xff)
				countB := countA + int((counts>>16)&0xff)
				countC := countB + int((counts>>32)&0xff)
				countD := countC + int((counts>>48)&0xff)

				oDataA.LookupOrZero(archsimd.LoadUint8x16Array(&compactLUT[mask&0x7fff])).StoreArray((*[16]uint8)(d))
				oDataB.LookupOrZero(archsimd.LoadUint8x16Array(&compactLUT[(mask>>16)&0x7fff])).StoreArray((*[16]uint8)(unsafe.Add(d, countA)))
				oDataC.LookupOrZero(archsimd.LoadUint8x16Array(&compactLUT[(mask>>32)&0x7fff])).StoreArray((*[16]uint8)(unsafe.Add(d, countB)))
				oDataD.LookupOrZero(archsimd.LoadUint8x16Array(&compactLUT[(mask>>48)&0x7fff])).StoreArray((*[16]uint8)(unsafe.Add(d, countC)))
				produced += countD
			}
		}
	}
	return consumed, produced, escFirst
}
func neonVectIsNonzero(v archsimd.Uint8x16) bool {
	return 0 != v.ReshapeToUint64s().SaturateToUint32().ReshapeToUint64s().GetElem(0)
}

func vld1q_u8_x4(s unsafe.Pointer) (archsimd.Uint8x16, archsimd.Uint8x16, archsimd.Uint8x16, archsimd.Uint8x16) {
	oDataA := archsimd.LoadUint8x16Array((*[16]uint8)(s))
	oDataB := archsimd.LoadUint8x16Array((*[16]uint8)(unsafe.Add(s, 16)))
	oDataC := archsimd.LoadUint8x16Array((*[16]uint8)(unsafe.Add(s, 32)))
	oDataD := archsimd.LoadUint8x16Array((*[16]uint8)(unsafe.Add(s, 48)))
	return oDataA, oDataB, oDataC, oDataD
}

func vtstq_u8(a, b archsimd.Uint8x16) archsimd.Mask8x16 {
	return a.And(b).NotEqual(broadcastZERO)
}

func addPairs(a, b archsimd.Uint8x16) archsimd.Uint8x16 {
	return a.ConcatEven(b).Add(a.ConcatOdd(b))
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
