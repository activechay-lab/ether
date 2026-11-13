package livetv

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

var hlsURI = regexp.MustCompile(`URI="([^"]+)"`)

// rewriteHLS resolves every URI against the final response URL after redirects,
// including encryption keys, initialization maps and rendition playlists. Tags
// such as DISCONTINUITY, BYTERANGE and MEDIA-SEQUENCE remain untouched.
func rewriteHLS(body []byte, base *url.URL, rewrite func(string) string) ([]byte, error) {
	resolve := func(raw string) (string, error) {
		u, err := url.Parse(raw)
		if err != nil {
			return "", err
		}
		u = base.ResolveReference(u)
		if !validHTTP(u.String()) {
			return "", fmt.Errorf("unsupported HLS URI")
		}
		return rewrite(u.String()), nil
	}
	lines := strings.Split(string(body), "\n")
	// UpLynk masters put EXT-X-START before their renditions. FFmpeg
	// versions in use treat that shared tag as a media-playlist declaration
	// and reject the following EXT-X-MEDIA/STREAM-INF. Our live join position
	// is controlled by the relay reserve, so omit this optional start hint
	// on masters only; media-playlist timing and encryption stay intact.
	master := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#EXT-X-STREAM-INF:") {
			master = true
			break
		}
	}
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if master && strings.HasPrefix(trim, "#EXT-X-START:") {
			lines[i] = ""
			continue
		}
		if trim == "" {
			continue
		}
		if !strings.HasPrefix(trim, "#") {
			u, err := resolve(trim)
			if err != nil {
				return nil, err
			}
			lines[i] = u
			continue
		}
		var rewriteErr error
		lines[i] = hlsURI.ReplaceAllStringFunc(line, func(m string) string {
			parts := hlsURI.FindStringSubmatch(m)
			u, err := resolve(parts[1])
			if err != nil {
				rewriteErr = err
				return m
			}
			return `URI="` + u + `"`
		})
		if rewriteErr != nil {
			return nil, rewriteErr
		}
	}
	return []byte(strings.Join(lines, "\n")), nil
}

func (s *Service) upstreamWithHeaders(ctx context.Context, raw string, r *http.Request, headers http.Header, source string, public ...http.Header) (*http.Response, error) {
	originURL, err := url.Parse(source)
	if err != nil {
		return nil, err
	}
	origin := relayOrigin(originURL)
	client := *s.o.HTTPClient
	scopeInputRedirects(&client, origin, headers, defaultUserAgent(), public...)
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if err != nil {
			return nil, err
		}
		if relayOrigin(req.URL) == origin {
			req.Header = headers.Clone()
		}
		if req.Header == nil {
			req.Header = make(http.Header)
		}
		if len(public) > 0 {
			applyMediaHeaders(req, public[0])
		}
		for _, name := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
			if v := r.Header.Get(name); v != "" {
				req.Header.Set(name, v)
			}
		}
		res, err := client.Do(req)
		if err == nil && res.StatusCode < 500 && res.StatusCode != 429 {
			return res, nil
		}
		if res != nil {
			res.Body.Close()
		}
		last = err
		if last == nil {
			last = fmt.Errorf("upstream unavailable")
		}
		if attempt < 2 {
			t := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
			select {
			case <-ctx.Done():
				t.Stop()
				return nil, ctx.Err()
			case <-t.C:
			}
		}
	}
	return nil, last
}

// ServeResource serves an init section or key of the session's owned
// timeline. Provider URLs are never addressable through it.
func (s *Service) ServeResource(w http.ResponseWriter, r *http.Request, id, key string, u *core.User) {
	ss, err := s.getSession(r.Context(), id, u)
	if err != nil {
		writeStreamError(w, err)
		return
	}
	if key == "index.m3u8" {
		s.ServePlaylist(w, r, id, u)
		return
	}
	ss.mu.Lock()
	lease := ss.owned
	ss.mu.Unlock()
	if lease == nil {
		writeStreamError(w, core.ErrNotFound)
		return
	}
	res, ok := lease.tl.resource(key)
	if !ok {
		writeStreamError(w, core.ErrNotFound)
		return
	}
	w.Header().Set("Content-Type", res.ctype)
	w.Header().Set("Content-Length", strconv.Itoa(len(res.data)))
	w.Header().Set("Cache-Control", "private, max-age=60")
	if r.Method != http.MethodHead {
		armWriteDeadline(w)
		w.Write(res.data)
	}
}

// resourceWriteTimeout bounds writing one buffered upstream object (up to
// 64 MiB held in memory) to the client. Servers have no WriteTimeout, so
// without it a client that stops reading would pin the buffer forever.
const resourceWriteTimeout = 5 * time.Minute

func armWriteDeadline(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(resourceWriteTimeout))
}
