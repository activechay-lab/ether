package plex

import "github.com/braedonsaunders/ether/internal/sessions"

func (s *Server) plexSources() sessions.PlexSources {
	return sessions.PlexSources{Playback: s.d.Playback, Log: s.log}
}
