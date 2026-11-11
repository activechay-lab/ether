package frontendadapter

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// Viewer-identity headers the proxy injects (spec §6.5). The plugin never
// sees an Ether credential; these are the only Ether-issued identity values
// it receives.
const (
	HeaderViewer     = "X-Ether-Viewer"
	HeaderViewerName = "X-Ether-Viewer-Name"
	HeaderLibraries  = "X-Ether-Libraries"
)

// forwardedRequestHeaders is the allowlist of client headers the proxy
// relays to a plugin. Anything not listed is dropped, so an adapter cannot
// be used to smuggle host-internal headers (Range is handled separately so
// media seeking works without trusting arbitrary values).
var forwardedRequestHeaders = []string{
	"Accept",
	"Accept-Language",
	"Accept-Encoding",
	"Content-Type",
	"If-Modified-Since",
	"If-None-Match",
	"User-Agent",
}

// forwardedResponseHeaders is the allowlist of plugin response headers the
// proxy relays to the client.
var forwardedResponseHeaders = []string{
	"Content-Type",
	"Content-Length",
	"Content-Disposition",
	"ETag",
	"Last-Modified",
	"Cache-Control",
	"Accept-Ranges",
	"Content-Range",
	"Vary",
}

// isEtherHeader reports whether a header name belongs to the reserved
// X-Ether-* namespace that must never cross the proxy boundary in either
// direction.
func isEtherHeader(name string) bool {
	return strings.HasPrefix(strings.ToLower(name), "x-ether-")
}

// isCredentialHeader reports whether a header carries a credential the
// plugin must never see.
func isCredentialHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "cookie", "x-api-key", "x-auth-token":
		return true
	default:
		return false
	}
}

// outboundHeaders builds the header set the plugin receives: the allowlist
// plus the injected viewer identity. Inbound X-Ether-* and credential
// headers are never copied.
func outboundHeaders(r *http.Request, auth authenticated) map[string]string {
	headers := make(map[string]string, len(forwardedRequestHeaders)+4)
	for _, name := range forwardedRequestHeaders {
		if v := r.Header.Get(name); v != "" {
			headers[name] = v
		}
	}
	if rng := r.Header.Get("Range"); rng != "" && validRange(rng) {
		headers["Range"] = rng
	}
	headers[HeaderViewer] = strconv.FormatInt(auth.viewer.ID, 10)
	headers[HeaderViewerName] = viewerName(auth.viewer)
	headers[HeaderLibraries] = libraryCSV(auth.libraries)
	return headers
}

// viewerName is the display name a plugin may show. Ether owns it; a plugin
// must not infer identity from anything else.
func viewerName(v *core.Viewer) string {
	if v == nil {
		return ""
	}
	if name := strings.TrimSpace(v.Title); name != "" {
		return name
	}
	return strings.TrimSpace(v.Username)
}

// libraryCSV renders the allowed library ids as a comma-separated list.
func libraryCSV(ids []core.LibraryID) string {
	if len(ids) == 0 {
		return ""
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(int64(id), 10)
	}
	return strings.Join(parts, ",")
}

// validRange accepts a single byte range so a plugin cannot be handed a
// header that would confuse its reader; malformed ranges are dropped and
// the plugin sees a full request.
func validRange(v string) bool {
	const prefix = "bytes="
	if !strings.HasPrefix(v, prefix) {
		return false
	}
	spec := strings.TrimPrefix(v, prefix)
	if spec == "" || strings.Contains(spec, ",") {
		return false
	}
	start, end, ok := strings.Cut(spec, "-")
	if !ok {
		return false
	}
	if start == "" && end == "" {
		return false
	}
	if start != "" {
		if _, err := strconv.ParseInt(start, 10, 64); err != nil {
			return false
		}
	}
	if end != "" {
		if _, err := strconv.ParseInt(end, 10, 64); err != nil {
			return false
		}
	}
	return true
}
