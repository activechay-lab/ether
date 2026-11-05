package livetv

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// broadcastHTTPSource is a session-bound loopback input for the cheap Matroska
// copy-remux. It attaches to the same long-lived TS mux at its live edge.
type broadcastHTTPSource struct {
	server   *http.Server
	listener net.Listener
	done     chan struct{}
	once     sync.Once
	url      string
}

func (s *Service) openBroadcastHTTPSource(ss *session) (*broadcastHTTPSource, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	token := randomID()
	source := &broadcastHTTPSource{listener: listener, done: make(chan struct{})}
	source.url = "http://" + listener.Addr().String() + "/" + token
	source.server = &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+token || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		index, err := s.broadcastSegments(r.Context(), ss)
		if err != nil {
			writeStreamError(w, err)
			return
		}
		reader, err := index.AttachTS(r.Context())
		if err != nil {
			writeStreamError(w, err)
			return
		}
		defer reader.Close()
		w.Header().Set("Content-Type", "video/mp2t")
		buffer := make([]byte, 32<<10)
		for {
			ss.mu.Lock()
			user, c, closed := ss.user, ss.info.Channel, ss.closed
			ss.mu.Unlock()
			if closed || ss.ctx.Err() != nil {
				return
			}
			ps, err := s.programmes(r.Context(), c, s.o.Now(), s.o.Now().Add(time.Second))
			var current *Programme
			if len(ps) > 0 {
				current = &ps[0]
			}
			if err != nil || !s.allowed(r.Context(), &user, c, current) {
				return
			}
			n, err := reader.Read(buffer)
			if n > 0 {
				if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
					return
				}
				if flush, ok := w.(http.Flusher); ok {
					flush.Flush()
				}
			}
			if err != nil {
				return
			}
		}
	})}
	go func() { defer close(source.done); _ = source.server.Serve(listener) }()
	return source, nil
}
func (s *broadcastHTTPSource) close() {
	s.once.Do(func() { s.server.Close(); s.listener.Close(); <-s.done })
}

type broadcastHTTPSession struct {
	core.HTTPSession
	source *broadcastHTTPSource
	once   sync.Once
}

func (s *broadcastHTTPSession) Close() error {
	s.once.Do(func() { s.HTTPSession.Close(); s.source.close() })
	return nil
}
func (s *Service) serveBroadcastHTTP(w http.ResponseWriter, r *http.Request, ss *session) error {
	// Reuse the existing single-flight per-viewer HTTP startup gate.
	for {
		ss.mu.Lock()
		if ss.closed {
			ss.mu.Unlock()
			return core.ErrNotFound
		}
		if stream := liveHTTP(ss); stream != nil {
			ss.mu.Unlock()
			stream.ServeHTTP(w, r)
			return nil
		}
		if wait := ss.httpStart; wait != nil {
			ss.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-r.Context().Done():
				return r.Context().Err()
			}
		}
		wait := make(chan struct{})
		ss.httpStart = wait
		info := ss.info.MediaInfo
		ss.mu.Unlock()
		source, err := s.openBroadcastHTTPSource(ss)
		var stream core.HTTPSession
		if err == nil {
			decision := core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, Container: "mkv", AudioStream: 1, SubtitleStream: -1}
			hs, cancel, startErr := s.startOwnedHLS(r.Context(), ss, core.HLSRequest{SessionID: "broadcast-http-" + ss.info.ID, InputURL: source.url, LiveInput: true, Info: *info, Decision: decision, Protocol: core.ProtocolHTTP})
			err = startErr
			if err == nil {
				var ok bool
				stream, ok = hs.(core.HTTPSession)
				if !ok {
					cancel()
					hs.Close()
					err = fmt.Errorf("broadcast HTTP remux unavailable")
				} else {
					stream = &broadcastHTTPSession{HTTPSession: &ownedBroadcastHTTP{HTTPSession: stream, cancel: cancel}, source: source}
				}
			}
			if err != nil {
				source.close()
			}
		}
		ss.mu.Lock()
		ss.httpStart = nil
		close(wait)
		closed := ss.closed
		if err == nil && !closed {
			ss.http = stream
		}
		ss.mu.Unlock()
		if err != nil {
			return err
		}
		if closed {
			stream.Close()
			return core.ErrNotFound
		}
		stream.ServeHTTP(w, r)
		return nil
	}
}

type ownedBroadcastHTTP struct {
	core.HTTPSession
	cancel context.CancelFunc
}

func (s *ownedBroadcastHTTP) Close() error { s.cancel(); return s.HTTPSession.Close() }
