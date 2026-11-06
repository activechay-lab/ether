package yenc

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/braedonsaunders/ether/internal/yenc/internal/rapidyenc"
)

// Exercise actual vector dispatch, not just output parity with a silently
// selected scalar implementation. The disabled build must consume nothing.
func TestSIMDDispatch(t *testing.T) {
	t.Logf("SIMD available: %v", rapidyenc.Available())
	src := bytes.Repeat([]byte{'a'}, 256)
	dst := make([]byte, len(src))
	consumed, produced, escaped := rapidyenc.Decode(dst, src, false)
	if rapidyenc.Available() {
		if consumed == 0 || produced != consumed || escaped {
			t.Fatalf("SIMD did not decode: %d %d %v", consumed, produced, escaped)
		}
		if !bytes.Equal(dst[:produced], bytes.Repeat([]byte{'a' - 42}, produced)) {
			t.Fatal("incorrect SIMD bytes")
		}
	} else if consumed != 0 || produced != 0 || escaped {
		t.Fatal("disabled SIMD consumed input")
	}
}

func TestDecodeLineSIMDBoundaries(t *testing.T) {
	rng := rand.New(rand.NewSource(346))
	for _, length := range []int{0, 1, 15, 16, 31, 32, 63, 64, 65, 127, 128, 129, 255, 256, 257, 1024} {
		for _, distribution := range []string{"random", "escapes", "consecutive", "plain"} {
			src := make([]byte, length)
			_, _ = rng.Read(src)
			switch distribution {
			case "escapes":
				for i := range src {
					src[i] = '='
				}
			case "consecutive":
				for i := range src {
					if i%5 < 3 {
						src[i] = '='
					}
				}
			case "plain":
				for i := range src {
					src[i] = 'a'
				}
			}
			for _, escaped := range []bool{false, true} {
				want, wantEsc := decodeLineScalar(src, escaped)
				// All offsets stress unaligned loads and both exact and prefix overlap.
				for _, offset := range []int{0, 1, 7, 15, 31, 63} {
					backing := make([]byte, length+offset+16)
					copy(backing[offset:], src)
					guard := bytes.Repeat([]byte{0xab}, 16)
					copy(backing[length+offset:], guard)
					n, gotEsc := DecodeLine(backing[:length], backing[offset:offset+length], escaped)
					if gotEsc != wantEsc || !bytes.Equal(backing[:n], want) {
						t.Fatalf("length=%d distribution=%s escaped=%v offset=%d: n=%d esc=%v", length, distribution, escaped, offset, n, gotEsc)
					}
					if !bytes.Equal(backing[length+offset:], guard) {
						t.Fatal("write beyond destination")
					}
				}
			}
		}
	}
}

func TestDecodeLineSIMDEveryByteAtEveryLane(t *testing.T) {
	for lane := range 129 {
		for value := range 256 {
			src := bytes.Repeat([]byte{'a'}, 192)
			src[lane] = byte(value)
			for _, escaped := range []bool{false, true} {
				want, wantEsc := decodeLineScalar(src, escaped)
				dst := make([]byte, len(src))
				n, gotEsc := DecodeLine(dst, src, escaped)
				if gotEsc != wantEsc || !bytes.Equal(dst[:n], want) {
					t.Fatalf("lane=%d byte=%d escaped=%v", lane, value, escaped)
				}
			}
		}
	}
}
