package plex

import (
	"github.com/braedonsaunders/ether/internal/core"
)

// sortFor maps a Plex sort key to a catalogue sort.
func sortFor(key, dir string) (core.SortOrder, bool) {
	so, desc, _, _ := sortSpecFor(key, dir)
	return so, desc
}
