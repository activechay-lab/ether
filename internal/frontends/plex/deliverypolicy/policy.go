package deliverypolicy

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/streamgrant"
	"net/http"
	"net/url"
	"strings"
)

func MediaSession(u *url.URL) (string, bool) {
	const prefix = "/video/:/transcode/universal/session/"
	if !strings.HasPrefix(u.Path, prefix) {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, prefix), "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", false
	}
	for _, part := range parts {
		if part == "." || part == ".." || len(part) > 128 {
			return "", false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
				return "", false
			}
		}
	}
	file := parts[2]
	if file != "index.m3u8" && file != "init.mp4" && file != "header" && !strings.HasSuffix(file, ".ts") && !strings.HasSuffix(file, ".m4s") && !strings.HasSuffix(file, ".mkv") {
		return "", false
	}
	return parts[0], true
}

var ForwardHeaders = []string{"Authorization", "Accept", "User-Agent", "X-Plex-Token", "X-Plex-Client-Identifier", "X-Plex-Client-Id", "X-Plex-Product", "X-Plex-Version", "X-Plex-Platform", "X-Plex-Platform-Version", "X-Plex-Device", "X-Plex-Device-Name", "X-Plex-Model", "X-Plex-Device-Vendor", "X-Plex-Language", "X-Plex-Features", "X-Plex-Client-Profile-Extra", "X-Plex-Client-Capabilities", "X-Plex-Session-Identifier", "X-Plex-Session-Id", "X-Plex-Container-Start", "X-Plex-Container-Size", "Range", "If-Range", "If-None-Match", "If-Modified-Since"}

const plexReadGrant = "X-Ether-Read-Token"

var accountCredentialFields = []string{"Authorization", "X-Plex-Token", "api_key", "apikey", "X-Emby-Token", "X-MediaBrowser-Token", "access_token"}

func GrantEnvelope(q *http.Request) bool {
	if (q.Method != "GET" && q.Method != "HEAD") || q.URL.RawPath != "" || len(q.URL.RequestURI()) > 16384 || q.Header.Get("X-Ether-Delivery-Hop") != "" {
		return false
	}
	if _, ok := MediaSession(q.URL); !ok {
		return false
	}
	for name := range q.Header {
		for _, field := range append([]string{streamgrant.StreamGrantParam, plexReadGrant}, accountCredentialFields...) {
			if strings.EqualFold(name, field) {
				return false
			}
		}
	}
	values, e := url.ParseQuery(q.URL.RawQuery)
	if e != nil {
		return false
	}
	found := false
	hasStream, hasRead := false, false
	for name, items := range values {
		for _, field := range accountCredentialFields {
			if strings.EqualFold(name, field) {
				return false
			}
		}
		for _, field := range []string{streamgrant.StreamGrantParam, plexReadGrant} {
			if !strings.EqualFold(name, field) {
				continue
			}
			if name != field || len(items) != 1 || items[0] == "" || len(items[0]) > 512 || strings.TrimSpace(items[0]) != items[0] {
				return false
			}
			for _, c := range items[0] {
				if c < 33 || c > 126 {
					return false
				}
			}
			found = true
			if field == streamgrant.StreamGrantParam {
				hasStream = true
			} else {
				hasRead = true
			}
		}
	}
	if hasRead && !hasStream && !boundedPlexClient(q, values) {
		return false
	}
	return found
}

func boundedPlexClient(q *http.Request, values url.Values) bool {
	for _, field := range []string{"X-Plex-Client-Identifier", "X-Plex-Client-Id"} {
		candidate := q.Header.Get(field)
		if candidate == "" {
			candidate = values.Get(field)
		}
		if candidate == "" {
			for name, items := range values {
				if strings.EqualFold(name, field) && len(items) == 1 {
					candidate = items[0]
					break
				}
			}
		}
		if candidate != "" {
			if len(candidate) > 512 || strings.TrimSpace(candidate) != candidate {
				return false
			}
			for _, c := range candidate {
				if c < 33 || c > 126 {
					return false
				}
			}
			return true
		}
	}
	return false
}

func Policy() core.DeliveryPolicy {
	return core.DeliveryPolicy{Session: MediaSession, GrantEnvelope: GrantEnvelope, Headers: ForwardHeaders}
}
