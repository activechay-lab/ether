package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// playerTap is a loopback reverse proxy between avprobe and Ether. AVPlayer
// exposes no request log of its own until an item is ready, so the tap
// records every request the player made (time, path, status, bytes, how
// long the response took) and the playlist bodies it received. It forwards
// bytes as they arrive and adds no buffering.
type playerTap struct {
	srv   *http.Server
	ln    net.Listener
	start time.Time
	mu    sync.Mutex
	log   bytes.Buffer
}

func newPlayerTap(target string, start time.Time) (*playerTap, string, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, "", err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	t := &playerTap{ln: ln, start: start}
	backend := &url.URL{Scheme: u.Scheme, Host: u.Host}
	proxy := httputil.NewSingleHostReverseProxy(backend)
	proxy.FlushInterval = -1
	proxy.ModifyResponse = func(resp *http.Response) error {
		if !strings.HasSuffix(resp.Request.URL.Path, ".m3u8") {
			return nil
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
		t.mu.Lock()
		fmt.Fprintf(&t.log, "  playlist at %.3fs (%s, %s):\n", time.Since(t.start).Seconds(), resp.Header.Get("Content-Type"), resp.Header.Get("Content-Encoding"))
		for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
			if i := strings.Index(line, "?"); i >= 0 && !strings.HasPrefix(line, "#") {
				line = line[:i] // drop the stream grant
			}
			fmt.Fprintf(&t.log, "    %s\n", line)
		}
		t.mu.Unlock()
		return err
	}
	t.srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		began := time.Since(t.start).Seconds()
		t.mu.Lock()
		fmt.Fprintf(&t.log, "%7.3fs > %s %s range=%q\n", began, r.Method, r.URL.Path, r.Header.Get("Range"))
		t.mu.Unlock()
		rec := &tapWriter{ResponseWriter: w, status: 200}
		proxy.ServeHTTP(rec, r)
		t.mu.Lock()
		fmt.Fprintf(&t.log, "%7.3fs %s %s range=%q -> %d %dB in %.3fs ua=%q\n", began, r.Method, r.URL.Path, r.Header.Get("Range"), rec.status, rec.bytes, time.Since(t.start).Seconds()-began, r.UserAgent())
		t.mu.Unlock()
	})}
	go func() { _ = t.srv.Serve(ln) }()
	u.Host = ln.Addr().String()
	return t, u.String(), nil
}

func (t *playerTap) Close(file string) {
	_ = t.srv.Close()
	t.mu.Lock()
	defer t.mu.Unlock()
	_ = os.WriteFile(file, t.log.Bytes(), 0o600)
}

type tapWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *tapWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }
func (w *tapWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}
func (w *tapWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
