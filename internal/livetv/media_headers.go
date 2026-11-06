package livetv

import (
	"net/http"
	"strings"
)

// Only explicit playback identity and referrer directives follow media to a
// provider-selected edge. Authentication, cookies and synthesized referring
// URLs remain confined to the source origin.
func redirectMediaHeaders(in map[string]string) http.Header {
	var h http.Header
	for k, v := range in {
		name := http.CanonicalHeaderKey(k)
		if (name == "User-Agent" || name == "Referer") && validInputHeader(name, v) {
			if h == nil {
				h = make(http.Header)
			}
			h.Set(name, v)
		}
	}
	return h
}
func applyMediaHeaders(req *http.Request, h http.Header) {
	if len(h) == 0 {
		return
	}
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	for k, v := range h {
		req.Header[k] = append([]string(nil), v...)
	}
}
func playlistMediaHeader(h *map[string]string, key, value string) {
	name := ""
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "_", "-")) {
	case "user-agent", "http-user-agent":
		name = "User-Agent"
	case "referer", "referrer", "http-referer", "http-referrer":
		name = "Referer"
	}
	value = strings.TrimSpace(value)
	if name == "" || !validInputHeader(name, value) {
		return
	}
	if *h == nil {
		*h = map[string]string{}
	}
	(*h)[name] = value
}
