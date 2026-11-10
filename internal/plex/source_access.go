package plex

import "github.com/braedonsaunders/ether/internal/frontend"

func (s *Server) plexSources() frontend.PlexSources {
	return s.d.SessionStores.NewPlexSources(s.d.Playback, s.log)
}
