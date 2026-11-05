package plex

import (
	"context"

	"github.com/braedonsaunders/ether/internal/plex/model"
)

// playlistsHub is PMS's "Recent Playlists" Home row (home.playlists): the
// user's playlists, most recently updated first. Omitted when there are
// none.
func (s *Server) playlistsHub(ctx context.Context, pr *Request, count int) (model.Hub, bool) {
	h, _, ok := s.playlistsHubProjection(ctx, pr, count)
	return h, ok
}
