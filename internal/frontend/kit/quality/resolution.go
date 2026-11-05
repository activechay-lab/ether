package quality

import "github.com/braedonsaunders/ether/internal/core"

// ResolutionLabel is the shared resolution label ("4K", "1080p", ...) for a
// coded picture size, so frontends badge a file exactly as Plex does.
func ResolutionLabel(width, height int) string { return core.ResolutionLabel(width, height) }
