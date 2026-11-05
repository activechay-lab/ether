package debrid

import (
	"net/http"
)

// NewStreamingTransport is an HTTP transport tuned for media streaming:
// keep-alive, HTTP/2 when offered, many idle connections per host and
// connect/TLS timeouts that tolerate a busy CDN edge.
func NewStreamingTransport() *http.Transport {
	return newStreamingTransport(nil)
}
