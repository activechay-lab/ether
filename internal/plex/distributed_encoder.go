package plex

import (
	"context"
	"net/http"

	"github.com/braedonsaunders/ether/internal/core"
)

// PlexEncoder is an opt-in trusted host seam implemented by the shared broker.
// Starts occur only after existing Plex authentication, owner gates, preparation
// and admission. The broker must revalidate the exact source revision and retain
// uncertain execution responsibility. It may choose local execution only before
// remote allocation; a returned error is never permission for Plex to retry local.
// Session/Stop true means the broker owns responsibility, even when Session is
// nil because start is pending. Transport errors must never turn true into false.
type PlexEncoder interface {
	StartPlexHLS(context.Context, *core.User, string, string, core.HLSRequest) (core.HLSSession, error)
	PlexHLSSession(string) (core.HLSSession, bool)
	StopPlexHLS(string) bool
}

// UsePlexEncoder installs an optional broker independent of playbackBroker.
// Keep it installed while releasing a mount: clearing playbackBroker does not
// relinquish pending remote encoder responsibility. Nil preserves local behavior.
func (s *Server) UsePlexEncoder(encoder PlexEncoder) {
	s.plexEncoderMu.Lock()
	s.plexEncoder = encoder
	s.plexEncoderMu.Unlock()
}
func (s *Server) currentPlexEncoder() PlexEncoder {
	s.plexEncoderMu.RLock()
	defer s.plexEncoderMu.RUnlock()
	return s.plexEncoder
}
func (s *Server) startPlexHLS(ctx context.Context, viewer *core.User, playID, id string, req core.HLSRequest) (core.HLSSession, error) {
	if encoder := s.currentPlexEncoder(); encoder != nil {
		return encoder.StartPlexHLS(ctx, viewer, playID, id, req)
	}
	if s.d.Transcoder == nil {
		return nil, core.ErrNotAvailable
	}
	return s.d.Transcoder.StartHLS(ctx, req)
}
func (s *Server) plexHLSSession(id string) (core.HLSSession, bool) {
	if encoder := s.currentPlexEncoder(); encoder != nil {
		if session, owned := encoder.PlexHLSSession(id); owned {
			return session, true
		}
	}
	if s.d.Transcoder == nil {
		return nil, false
	}
	return s.d.Transcoder.Session(id)
}
func (s *Server) stopPlexHLS(id string) {
	if encoder := s.currentPlexEncoder(); encoder != nil && encoder.StopPlexHLS(id) {
		return
	}
	if s.d.Transcoder != nil {
		s.d.Transcoder.Stop(id)
	}
}

// forwardPlexMedia runs only inside an already-authorized public media handler.
// The typed private receiver must replay the ORIGINAL public handler (including
// token/grant/policy middleware); mTLS peer trust is never viewer authorization.
func (s *Server) forwardPlexMedia(w http.ResponseWriter, r *http.Request, id string) bool {
	if forward, ok := s.currentPlexEncoder().(interface {
		ForwardPlexMedia(http.ResponseWriter, *http.Request, string) bool
	}); ok {
		return forward.ForwardPlexMedia(w, r, id)
	}
	return false
}
