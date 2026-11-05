// These helpers support offline acceptance tests across package boundaries.
// Runtime callers use the production APIs in the other files.
package archive

import (
	"context"

	"io"
)

// List7z parses a 7z archive. r is the full archive (for a multi-volume
// .7z.001/.002… set pass the concatenation, e.g. a MultiReader; SplitExtents
// maps the result back onto the individual volumes). Extents use Volume 0
// and offsets in r. Only stored (Copy-only) members have extents.
func List7z(r io.ReaderAt, size int64) ([]Member, error) {
	return list7z(&sz7Crypto{ctx: context.Background()}, r, size)
}
