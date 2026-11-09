package experience

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

// SearchSources are the parts of search every frontend shares: the
// catalogue (with the owner's availability rules applied), natural-language
// search, the viewer's affinity and collection readings. The app builds one
// set; each frontend adds only its own visibility, so a query finds the same
// titles in the same order on Plex, Jellyfin, Emby and the rest.
type SearchSources struct {
	Catalog  func(context.Context, string, []core.MediaType, int) ([]core.Title, error)
	Natural  func(context.Context, *core.User, string, int) ([]core.Title, bool)
	Familiar func(context.Context, *core.User, []core.RatingKey) map[core.RatingKey]int
	Vibe     VibeSource
	// Details reads in full the titles a candidate search returned partial
	// (core.SearchCandidates). Nil leaves every search reading its results
	// in full.
	Details func(context.Context, []core.Title) ([]core.Title, error)
}

// For is the shared search seen through one frontend's visibility.
// unrestricted is the trusted host policy capability (nil fails closed).
func (src SearchSources) For(vis Visibility, unrestricted func(*core.User) bool) Search {
	return Search{
		Catalog: src.Catalog, Natural: src.Natural, Familiar: src.Familiar, Details: src.Details,
		Visibility: vis, Unrestricted: unrestricted,
		Vibe: VibeSearch{Source: src.Vibe, Visibility: vis, Unrestricted: unrestricted},
	}
}
