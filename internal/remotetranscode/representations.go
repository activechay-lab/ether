package remotetranscode

import (
	"math"
	"net/http"

	"github.com/braedonsaunders/ether/internal/core"
)

func (a *Adapter) SupportsRequest(r core.HLSRequest) bool {
	return SupportsRequest(r, a.Capabilities())
}

func mediaOperation(op string) bool {
	switch op {
	case "playlist", "segment", "rung-playlist", "rung-segment", "manifest", "init", "media", "http", "soft-subtitle":
		return true
	}
	return false
}

func validRungs(rungs []core.HLSRung) bool {
	if len(rungs) > 32 {
		return false
	}
	seen := make(map[string]bool, len(rungs))
	for i, r := range rungs {
		if !token(r.ID) || seen[r.ID] || (i == 0 && r.ID != "base") || len(r.Codecs) > 256 || r.Bandwidth < 0 || r.AverageBandwidth < 0 || r.Width < 0 || r.Height < 0 || math.IsNaN(r.FrameRate) || math.IsInf(r.FrameRate, 0) || r.FrameRate < 0 {
			return false
		}
		seen[r.ID] = true
	}
	return true
}

// Rungs is the backend's bounded immutable ladder, never a synthesized rendition.
func (s *Session) Rungs() []core.HLSRung {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]core.HLSRung(nil), s.rungs...)
}
func (s *Session) ServeRungPlaylist(w http.ResponseWriter, r *http.Request, rung string) {
	s.serveRepresentation(w, r, message{Op: "rung-playlist", Rung: rung})
}
func (s *Session) ServeRungSegment(w http.ResponseWriter, r *http.Request, rung string, n int) {
	s.serveRepresentation(w, r, message{Op: "rung-segment", Rung: rung, Segment: n})
}
func (s *Session) ServeManifest(w http.ResponseWriter, r *http.Request) {
	s.serveRepresentation(w, r, message{Op: "manifest"})
}
func (s *Session) ServeInit(w http.ResponseWriter, r *http.Request, rep int) {
	s.serveRepresentation(w, r, message{Op: "init", Representation: rep})
}
func (s *Session) ServeMedia(w http.ResponseWriter, r *http.Request, rep, n int) {
	s.serveRepresentation(w, r, message{Op: "media", Representation: rep, Segment: n})
}
func (s *Session) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.serveRepresentation(w, r, message{Op: "http"})
}

func (w *Worker) serveMedia(out http.ResponseWriter, r *http.Request, m message, s core.OwnedEncoderSession, protocol string, rungs []core.HLSRung) {
	switch m.Op {
	case "playlist":
		s.ServePlaylist(out, r)
		return
	case "segment":
		s.ServeSegment(out, r, m.Segment)
		return
	case "rung-playlist", "rung-segment":
		if w.opts.Capabilities.ABR && protocol != core.ProtocolHTTP {
			if ladder, ok := s.(core.HLSLadderSession); ok {
				// Only advertised rungs can cause encoder work.
				for _, rung := range rungs {
					if rung.ID == m.Rung {
						if m.Op == "rung-playlist" {
							ladder.ServeRungPlaylist(out, r, m.Rung)
						} else {
							ladder.ServeRungSegment(out, r, m.Rung, m.Segment)
						}
						return
					}
				}
			}
		}
	case "manifest", "init", "media":
		if w.opts.Capabilities.DASH && protocol == core.ProtocolDASH {
			if dash, ok := s.(core.DASHSession); ok {
				switch m.Op {
				case "manifest":
					dash.ServeManifest(out, r)
				case "init":
					dash.ServeInit(out, r, m.Representation)
				case "media":
					dash.ServeMedia(out, r, m.Representation, m.Segment)
				}
				return
			}
		}
	case "soft-subtitle":
		if w.opts.Capabilities.SoftSubtitles && protocol != core.ProtocolHTTP {
			if v, ok := s.(interface {
				ServeSoftSubtitle(http.ResponseWriter, *http.Request, int) bool
			}); ok && v.ServeSoftSubtitle(out, r, m.Subtitle) {
				return
			}
		}
	case "http":
		if w.opts.Capabilities.HTTP && protocol == core.ProtocolHTTP {
			if stream, ok := s.(core.HTTPSession); ok {
				stream.ServeHTTP(out, r)
				return
			}
		}
	}
	http.Error(out, "unsupported representation", http.StatusUnprocessableEntity)
}

var _ core.HLSLadderSession = (*Session)(nil)
var _ core.DASHSession = (*Session)(nil)
var _ core.HTTPSession = (*Session)(nil)

// Progressive output must reach the viewer while the upstream encoder runs.
type flushingWriter struct{ http.ResponseWriter }

func (w flushingWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if err == nil {
		err = http.NewResponseController(w.ResponseWriter).Flush()
	}
	return n, err
}

// The exact backend owns this session-wide claim, including every advertised rung.
func (s *Session) IndependentSegments() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.independent }

var _ core.HLSIndependentSegments = (*Session)(nil)
