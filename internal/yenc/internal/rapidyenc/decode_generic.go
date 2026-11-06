//go:build !goexperiment.simd || (!amd64 && !arm64)

package rapidyenc

func Available() bool                                       { return false }
func Decode(dst, src []byte, escaped bool) (int, int, bool) { return 0, 0, escaped }
