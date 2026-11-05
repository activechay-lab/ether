package remotetranscode

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func softSubtitle(info core.MediaInfo, index int) bool {
	if index < 0 || index > 100000 {
		return false
	}
	for _, s := range info.Streams {
		if s.Index == index && s.Type == "subtitle" && !s.External && s.ExternalURL == "" {
			switch strings.ToLower(s.Codec) {
			case "ass", "ssa", "subrip", "srt", "webvtt", "mov_text", "text":
				return true
			}
		}
	}
	return false
}

// Touch and PlaybackState use bounded explicit RPCs; there is no background loop.
// HTTP sessions support Touch but have no local PlaybackState contract.
func (s *Session) Touch() { s.control("touch", "") }
func (s *Session) PlaybackState(state string) {
	if state == "playing" || state == "paused" || state == "stopped" {
		s.control("playback-state", state)
	}
}
func (s *Session) control(op, state string) {
	if !s.adapter.Capabilities().Controls {
		return
	}
	s.mu.Lock()
	stopped := s.stopped
	s.mu.Unlock()
	if stopped {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := s.adapter.call(ctx, message{Op: op, State: state, Identity: s.id}, nil)
	if err != nil {
		return
	}
	defer r.Body.Close()
	if s.observe(r) == nil {
		s.touch()
	}
}

// ServeSoftSubtitle attaches only to this execution's selected embedded ASS
// output. It never extracts via an additional URL, source reader or process.
func (s *Session) ServeSoftSubtitle(w http.ResponseWriter, r *http.Request, index int) bool {
	if r.Method != http.MethodGet || !s.adapter.Capabilities().SoftSubtitles || index < 0 || index > 100000 {
		return false
	}
	s.mu.Lock()
	stopped := s.stopped
	s.mu.Unlock()
	if stopped {
		return false
	}
	response, err := s.adapter.call(r.Context(), message{Op: "soft-subtitle", Identity: s.id, Subtitle: index, Head: r.Method == http.MethodHead}, r.Header)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false
	}
	for _, h := range []string{"Content-Type", "Content-Length", "Cache-Control", "ETag", "Last-Modified"} {
		if v := response.Header.Values(h); len(v) > 0 {
			w.Header()[h] = v
		}
	}
	w.WriteHeader(response.StatusCode)
	s.touch()
	if r.Method != http.MethodHead {
		_, _ = io.Copy(flushingWriter{w}, response.Body)
	}
	return true
}
