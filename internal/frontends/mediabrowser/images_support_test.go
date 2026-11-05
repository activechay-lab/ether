package mediabrowser

import (
	"github.com/braedonsaunders/ether/internal/frontend"

	"net/http"
)

// gridImages issues bounded, policy-checked grants without loading item
// details or resolving playback versions. An unavailable batch is omitted.
func (i *instance) gridImages(r *http.Request, keys []frontend.RatingKey) map[frontend.RatingKey]map[string]any {
	return i.gridImageRows(r, nil, keys, nil)
}
