package stremio

import (
	"github.com/braedonsaunders/ether/internal/core"
)

// convert turns addon streams into releases with default options (tests,
// fuzzing).
func convert(streams []stream, req core.SearchRequest, packs bool) (out []core.Release, skippedNoHash, skippedAmbiguous int) {
	return convertWith(streams, req, packs, convertOptions{})
}
