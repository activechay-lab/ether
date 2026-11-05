package sessions

import (
	"github.com/braedonsaunders/ether/internal/core"
)

// PlexStreamSlot is the stable identity of a stream within a title.
// A subtitle's id is its language, codec, and source (the sidecar URL, or
// embedded), not its position in a variant's stream list. Play Version
// reorders those lists as candidates warm, and the same part id can name a
// different release; the viewer's choice has to keep naming the same track.
func PlexStreamSlot(index int, info core.StreamInfo) string { return plexStreamSlot(index, info) }
