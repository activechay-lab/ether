// Package remote is the remote plugin runtime (SPEC §5.3, WS8b).
//
// A remote plugin is an HTTP-shaped JSON service. Ether reaches it two
// ways, with identical request and response shapes:
//
//   - URL mode: Ether fetches <base><path> over HTTP(S) through
//     internal/safefetch (public addresses only, or exactly one
//     owner-confirmed LAN origin) and internal/egress. Any Stremio
//     add-on is a remote plugin in this mode.
//   - Tunnel mode: a sidecar opens one outbound websocket to Ether at
//     GET /ether/plugins/tunnel, authenticated with a plugin
//     credential. Ether multiplexes HTTP-shaped requests over that
//     socket, so the sidecar needs no inbound ports and works behind
//     NAT. Byte ranges for files the sidecar serves are relayed over
//     the tunnel in chunks.
//
// The transport types below are the wire shapes (multi-value headers,
// byte bodies). factory.go is the one place they meet the plugin
// host's contract in internal/plugins (plugins.Request,
// plugins.Runtime, plugins.Host), converting both ways so the rest of
// this package stays transport-only.
package remote

import (
	"context"
	"crypto/rand"

	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend/kit/authhdr"
)

// Header names Ether sends to every remote plugin (SPEC §5.1.2).
const (
	HeaderRequestID  = "X-Ether-Request-Id"
	HeaderDeadline   = "X-Ether-Deadline"
	HeaderLocale     = "X-Ether-Locale"
	HeaderCredential = "X-Ether-Plugin-Credential"
	// HeaderPick carries a subtitle pick token (SPEC §5.1.2). Callers
	// set it through Request.Headers; the constant keeps the spelling
	// in one place.
	HeaderPick = "X-Ether-Pick"
)

// TunnelProtocol is the tunnel wire protocol version. It is sent as
// the websocket subprotocol and in the hello/welcome frames.
const TunnelProtocol = "ether-tunnel-v1"

// TunnelPath is where sidecars connect. HostPath is where remote
// plugins call Ether's host services.
const (
	TunnelPath = "/ether/plugins/tunnel"
	HostPath   = "/ether/plugin-host/"
)

var (
	// ErrTooLarge reports a response body over the configured cap.
	ErrTooLarge = errors.New("remote: response body too large")
	// ErrDisconnected reports a tunnel connection lost while a call
	// was in flight, or a call made with no sidecar connected.
	ErrDisconnected = errors.New("remote: tunnel disconnected")
	// ErrUnauthorized reports a missing or rejected plugin credential.
	ErrUnauthorized = errors.New("remote: unauthorized")
	// ErrPermissionDenied is returned by Host implementations when a
	// plugin lacks the grant a host service needs (SPEC §5.4).
	ErrPermissionDenied = errors.New("remote: permission denied")
	// ErrNotFound is returned by Host implementations for missing data.
	ErrNotFound = errors.New("remote: not found")
	// ErrInvalid is returned by Host implementations for bad input.
	ErrInvalid = errors.New("remote: invalid request")
	// ErrUnavailable is returned when a service is not wired yet.
	ErrUnavailable = errors.New("remote: unavailable")
	// ErrClosed reports use of a closed runtime.
	ErrClosed = errors.New("remote: closed")
)

// Request is one HTTP-shaped plugin call. Path is a plugin route such
// as "/manifest.json", "/stream/movie/tt123.json" or
// "/ether/configure"; it must be a path, never an absolute URL.
type Request struct {
	Method  string
	Path    string
	Query   map[string][]string
	Headers map[string][]string
	Body    []byte
	// Locale, when set, overrides the client's configured locale for
	// this call.
	Locale string
	// RequestID correlates the call in logs; generated when empty.
	RequestID string
	// Timeout bounds this call. When zero, the context deadline (or
	// the client default) applies. The resulting absolute deadline is
	// sent to the plugin as X-Ether-Deadline.
	Timeout time.Duration
}

// Response is one buffered plugin reply.
type Response struct {
	Status  int
	Headers map[string][]string
	Body    []byte
}

// Header returns the first value of name, or "".
func (r Response) Header(name string) string {
	return firstHeader(r.Headers, name)
}

// StreamResponse is a plugin reply whose body streams. The caller
// must close Body; closing it cancels the underlying call.
type StreamResponse struct {
	Status  int
	Headers map[string][]string
	Body    io.ReadCloser
}

// Header returns the first value of name, or "".
func (s *StreamResponse) Header(name string) string {
	if s == nil {
		return ""
	}
	return firstHeader(s.Headers, name)
}

// Runtime is the transport WS7's host talks through: one HTTP-shaped
// JSON call, plus Close. URL clients and tunnel runtimes both
// implement it.
type Runtime interface {
	Call(ctx context.Context, req Request) (Response, error)
	Close() error
}

// Streamer is implemented by runtimes that can relay a response body
// without buffering it (byte-range file relay in both modes).
type Streamer interface {
	CallStream(ctx context.Context, req Request) (*StreamResponse, error)
}

// CredentialResolver authenticates a plugin credential and returns
// the plugin id it belongs to. WS7 mints credentials at pairing time
// as identity_grants rows on surface "plugin"; this package only
// needs the lookup, so the host supplies it.
type CredentialResolver interface {
	ResolvePluginCredential(ctx context.Context, credential string) (string, error)
}

// CredentialResolverFunc adapts a function to CredentialResolver.
type CredentialResolverFunc func(ctx context.Context, credential string) (string, error)

// ResolvePluginCredential calls f.
func (f CredentialResolverFunc) ResolvePluginCredential(ctx context.Context, credential string) (string, error) {
	return f(ctx, credential)
}

// credentialSubprotocolPrefix carries a credential for websocket
// clients that cannot set upgrade headers (browser-style WebSocket
// in Node): Sec-WebSocket-Protocol: ether-credential.<token>.
const credentialSubprotocolPrefix = "ether-credential."

// credentialFromRequest extracts a plugin credential from a request:
// the dedicated header first, then a bearer token, then the tunnel
// credential subprotocol.
func credentialFromRequest(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get(HeaderCredential)); v != "" {
		return v
	}
	token, ok := authhdr.Bearer(r.Header.Get("Authorization"))
	if ok {
		return token
	}
	for _, header := range r.Header.Values("Sec-Websocket-Protocol") {
		for _, part := range strings.Split(header, ",") {
			if p := strings.TrimSpace(part); strings.HasPrefix(p, credentialSubprotocolPrefix) {
				return strings.TrimPrefix(p, credentialSubprotocolPrefix)
			}
		}
	}
	return ""
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// validatePath rejects anything that is not a plain absolute path on
// the plugin: absolute URLs, scheme-relative targets and ".." hops
// would let a confused caller escape the plugin's base.
func validatePath(p string) error {
	if p == "" {
		return fmt.Errorf("%w: empty path", ErrInvalid)
	}
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return fmt.Errorf("%w: path %q must start with one slash", ErrInvalid, p)
	}
	if strings.Contains(p, "://") || strings.Contains(p, "\\") {
		return fmt.Errorf("%w: path %q is not a plugin route", ErrInvalid, p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("%w: path %q escapes the plugin base", ErrInvalid, p)
		}
	}
	return nil
}

func firstHeader(h map[string][]string, name string) string {
	if h == nil {
		return ""
	}
	if v, ok := h[http.CanonicalHeaderKey(name)]; ok && len(v) > 0 {
		return v[0]
	}
	for k, v := range h {
		if strings.EqualFold(k, name) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

func cloneHeaders(h map[string][]string) map[string][]string {
	if h == nil {
		return nil
	}
	out := make(map[string][]string, len(h))
	for k, v := range h {
		out[http.CanonicalHeaderKey(k)] = append([]string(nil), v...)
	}
	return out
}

func cloneQuery(q map[string][]string) url.Values {
	out := url.Values{}
	for k, v := range q {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// deadlineFor derives the call context and the absolute deadline to
// advertise. A request timeout narrows the context; a context
// deadline already tighter than the client default wins as is.
func deadlineFor(ctx context.Context, req Request, def time.Duration) (context.Context, context.CancelFunc, time.Time) {
	timeout := req.Timeout
	if timeout <= 0 {
		if _, ok := ctx.Deadline(); !ok {
			timeout = def
		}
	}
	if timeout > 0 {
		c, cancel := context.WithTimeout(ctx, timeout)
		dl, _ := c.Deadline()
		return c, cancel, dl
	}
	dl, _ := ctx.Deadline()
	return ctx, func() {}, dl
}

// etherHeaders returns the headers every plugin call carries: the
// caller's own headers plus the X-Ether-* set, which always wins so a
// caller cannot forge another plugin's credential or deadline.
func etherHeaders(req Request, requestID, locale, credential string, deadline time.Time) map[string][]string {
	h := cloneHeaders(req.Headers)
	if h == nil {
		h = map[string][]string{}
	}
	h[HeaderRequestID] = []string{requestID}
	if !deadline.IsZero() {
		h[HeaderDeadline] = []string{deadline.UTC().Format(time.RFC3339)}
	} else {
		delete(h, HeaderDeadline)
	}
	if locale != "" {
		h[HeaderLocale] = []string{locale}
	}
	if credential != "" {
		h[HeaderCredential] = []string{credential}
	} else {
		delete(h, HeaderCredential)
	}
	return h
}
