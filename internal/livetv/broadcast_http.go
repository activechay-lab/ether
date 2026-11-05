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

		latest, ok := index.Latest()
		after := int64(-1)
		if ok {
			after = latest.Seq - 1
		}
		w.Header().Set("Content-Type", "video/mp2t")
		for {
			seg, err := index.Wait(r.Context(), after)
			if err != nil || !s.broadcastSegmentAllowed(r.Context(), ss, seg) {
				return
			}
			ss.mu.Lock()
			closed := ss.closed
			ss.mu.Unlock()
			if closed || ss.ctx.Err() != nil {
				return
			}
			partRequest := r.Clone(r.Context())
			partRequest.Header = r.Header.Clone()
			partRequest.Header.Del("Range")
			index.ServeTS(&broadcastTSWriter{ResponseWriter: w, header: make(http.Header)}, partRequest, seg.Seq)
			if flush, ok := w.(http.Flusher); ok {
				flush.Flush()
			}
			after = seg.Seq
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
			s.serveBroadcastHTTPMedia(w, r, ss, stream)
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
		if info == nil {
			ss.httpStart = nil
			close(wait)
			ss.mu.Unlock()
			return core.ErrNotAvailable
		}
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
		s.serveBroadcastHTTPMedia(w, r, ss, stream)
		return nil
	}
}

type ownedBroadcastHTTP struct {
	core.HTTPSession
	cancel context.CancelFunc
}

func (s *ownedBroadcastHTTP) Close() error { s.cancel(); return s.HTTPSession.Close() }

type broadcastHTTPObservation struct{ *mediaObservation }

func (w *broadcastHTTPObservation) Write(p []byte) (int, error) {
	n, err := w.mediaObservation.Write(p)
	if n > 0 && err == nil {
		w.ss.mu.Lock()
		w.ss.last = w.s.o.Now()
		w.ss.firstMediaDelivered = true
		w.ss.mu.Unlock()
	}
	return n, err
}
func (s *Service) serveBroadcastHTTPMedia(w http.ResponseWriter, r *http.Request, ss *session, stream core.HTTPSession) {
	s.touchMedia(ss)
	observed := s.observeMedia(w, ss)
	defer observed.done()
	stream.ServeHTTP(&broadcastHTTPObservation{observed}, r)
}

// Segment serving uses ServeContent. Its per-file Content-Length and status
// must not become the headers of the continuous Matroska input stream.
type broadcastTSWriter struct {
	http.ResponseWriter
	header http.Header
}

func (w *broadcastTSWriter) Header() http.Header { return w.header }
func (w *broadcastTSWriter) WriteHeader(int)     {}
