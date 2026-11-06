//go:build cgo || !goexperiment.simd || (!amd64 && !arm64)

package yenc

func rapidEnabled() bool  { return false }
func rapidKernel() string { return "SWAR" }
func decodeRapidBody(lr LineReader, o DecodeOptions, p *Part) (*Part, error) {
	return decodeBodySWAR(lr, o, p, nil, true, false, bodyWireBudget(o.MaxSize))
}
