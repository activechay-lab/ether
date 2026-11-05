// These helpers support offline acceptance tests across package boundaries.
// Runtime callers use the production APIs in the other files.
package archive

import (
	"fmt"
)

// ListRAR reads every volume (in order) with ReadRARVolume and composes the
// members. Extent volume indexes refer to vols.
func ListRAR(vols []Volume) ([]Member, error) {
	parsed := make([]*RARVolume, len(vols))
	for i, v := range vols {
		if v.R == nil {
			return nil, fmt.Errorf("archive: volume %d: %w", i, ErrMissingVolume)
		}
		rv, err := ReadRARVolume(v.R, v.Size, nil)
		if err != nil {
			return nil, fmt.Errorf("archive: volume %d: %w", i, err)
		}
		parsed[i] = rv
	}
	return ComposeRAR(parsed)
}
