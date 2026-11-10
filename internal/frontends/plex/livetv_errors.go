package plex

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

func (s *Server) writeLiveTuneError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
		return
	}
	detail := frontend.DiagnoseLiveTV("tune", err)
	status := http.StatusServiceUnavailable
	if errors.Is(err, frontend.ErrNotFound) {
		status = http.StatusNotFound
	}
	if errors.Is(err, frontend.ErrUnauthorized) {
		status = http.StatusUnauthorized
	}
	w.Header().Set("X-Ether-LiveTV-Error", detail.Code)
	if detail.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int((detail.RetryAfter+time.Second-1)/time.Second)))
	}
	// A tune fails before the universal decision endpoint can explain it.
	// Return the live reason in the negotiated Plex format, never VOD copy.
	s.writeStatus(w, r, status, &model.MediaContainer{Size: 0, Extra: []model.Attr{
		model.A("code", status), model.A("message", detail.Message), model.A("status", detail.Message),
	}})
}
