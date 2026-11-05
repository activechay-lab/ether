// These helpers support offline acceptance tests across package boundaries.
// Runtime callers use the production APIs in the other files.
package libraryfilter

import (
	"github.com/braedonsaunders/ether/internal/core"
)

// Compile treats every rule as hide and leaves unmatched titles visible.
func Compile(specs []core.LibraryExclusion) (*Filter, error) {
	return CompilePolicy(specs, "show")
}
