package archive

import (
	"fmt"

	"math"
	"sort"
)

// SplitExtents maps extents expressed as offsets in the concatenated stream
// (Volume must be 0, as returned by List7z over a MultiReader) onto extents
// of the individual volumes passed to NewMultiReader. Volumes with Size <= 0
// keep their index but never receive extents.
func SplitExtents(vols []Volume, exts []Extent) ([]Extent, error) {
	// Index once instead of scanning from volume zero for each member. Large
	// season packs can have thousands of both volumes and members. Keep empty
	// volumes out of the search while preserving the original volume indexes.
	type volumeSpan struct {
		index      int
		start, end int64
	}
	index := make([]volumeSpan, 0, len(vols))
	var total int64
	for vi, v := range vols {
		if v.Size <= 0 {
			continue
		}
		if v.Size > math.MaxInt64-total {
			return nil, fmt.Errorf("archive: concatenated volume size overflows: %w", ErrCorrupt)
		}
		index = append(index, volumeSpan{vi, total, total + v.Size})
		total += v.Size
	}
	out := make([]Extent, 0, len(exts))
	for _, e := range exts {
		if e.Volume != 0 || e.Offset < 0 || e.Size < 0 {
			return nil, fmt.Errorf("archive: extent %+v is not a concatenated-stream extent: %w", e, ErrCorrupt)
		}
		if e.Offset > total || e.Size > total-e.Offset {
			return nil, fmt.Errorf("archive: extent %+v runs past the last volume: %w", e, ErrMissingVolume)
		}
		off, rem := e.Offset, e.Size
		vi := sort.Search(len(index), func(i int) bool { return index[i].end > off })
		for rem > 0 {
			v := index[vi]
			n := min(v.end-off, rem)
			out = append(out, Extent{Volume: v.index, Offset: off - v.start, Size: n})
			off += n
			rem -= n
			vi++
		}
	}
	return out, nil
}
