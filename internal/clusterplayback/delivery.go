package clusterplayback

import (
	"context"
	"github.com/braedonsaunders/ether/internal/generationfence"
	"github.com/braedonsaunders/ether/internal/nodewire/outbound"
	"io"
	"net/http"
	"net/url"
	"time"
)

const DeliveryPath = "/cluster/playback/delivery"
const deliveryHop = "X-Ether-Delivery-Hop"
const deliveryURI = "X-Ether-Delivery-URI"
const deliveryProtocol = "X-Ether-Delivery-Protocol"

var deliveryHeaders = []string{"Allow", "Vary", "Content-Security-Policy", "X-Content-Type-Options", "Access-Control-Allow-Origin", "Access-Control-Allow-Methods", "Access-Control-Allow-Headers", "Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Cache-Control", "ETag", "Last-Modified", "Content-Disposition"}

// BindMediaBrowserDelivery replays the original mounted Jellyfin/Emby
// handlers, including their protocol policy and representation headers.
func (r *Runtime) BindMediaBrowserDelivery(handler http.Handler) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if handler == nil || r.mediaBrowserDelivery != nil || r.closed {
		return ErrDenied
	}
	r.mediaBrowserDelivery = handler
	return nil
}

// BindDelivery is called once by the broker constructor before listeners start.
// The callback retains all existing grant, viewer and current media checks.
func (r *Runtime) BindDelivery(handler func(http.ResponseWriter, *http.Request, string)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if handler == nil || r.delivery != nil {
		return ErrDenied
	}
	r.delivery = handler
	return nil
}
func (r *Runtime) deliveryHTTP(w http.ResponseWriter, q *http.Request) {
	peer, e := r.o.Guard.Request(q)
	if e != nil || !r.mediaPeer(q.Context(), peer) {
		http.Error(w, "denied", 401)
		return
	}
	if q.Method != "GET" && q.Method != "HEAD" && q.Method != "OPTIONS" {
		http.Error(w, "method", 405)
		return
	}
	if len(q.Header.Values(deliveryHop)) != 1 || q.Header.Get(deliveryHop) != "1" || len(q.Header.Values(deliveryURI)) != 1 {
		http.Error(w, "scope", 400)
		return
	}
	raw := q.Header.Get(deliveryURI)
	if len(raw) > 8192 {
		http.Error(w, "scope", 400)
		return
	}
	u, e := url.ParseRequestURI(raw)
	if e != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || u.RawPath != "" {
		http.Error(w, "scope", 400)
		return
	}
	_, id, pathProtocol, ok := neutralDeliveryRoute(u.Path)
	protocol := q.Header.Get(deliveryProtocol)
	if !ok || len(q.Header.Values(deliveryProtocol)) != 1 || len(protocol) > 32 || (pathProtocol != "" && pathProtocol != protocol) {
		http.Error(w, "scope", 400)
		return
	}

	if q.Method == "OPTIONS" && pathProtocol == "" {
		http.Error(w, "method", 405)
		return
	}
	coordinator, e := r.CurrentCoordinator(q.Context(), id)
	if e != nil || coordinator.Identity != r.o.Self {
		http.NotFound(w, q)
		return
	}
	rt, e := r.route(q.Context(), id)
	if e != nil {
		http.NotFound(w, q)
		return
	}
	r.mu.Lock()
	handler := r.delivery
	mediaBrowser := r.mediaBrowserDelivery
	r.mu.Unlock()
	if (pathProtocol != "" && mediaBrowser == nil) || (pathProtocol == "" && handler == nil) {
		http.Error(w, "unavailable", 503)
		return
	}
	clone, e := restoreOrigin(q, u, raw)
	if e != nil {
		http.Error(w, "scope", 400)
		return
	}
	if pathProtocol != "" {
		mediaBrowser.ServeHTTP(r.fencedDelivery(w, q, rt), clone)
		return
	}
	handler(r.fencedDelivery(w, q, rt), clone, protocol)
}

// ForwardMedia performs at most one indexed ownership hop. No redirect or private
// endpoint is returned to a client. The owning broker authenticates the grant.
func (r *Runtime) ForwardMedia(w http.ResponseWriter, q *http.Request, id, protocol string) bool {
	if q.Header.Get(deliveryHop) != "" {
		return false
	}
	coordinator, e := r.CurrentCoordinator(q.Context(), id)
	if e != nil || coordinator.Identity == r.o.Self {
		return false
	}
	if q.Method != "GET" && q.Method != "HEAD" && !mediaBrowserPreflight(q) {
		http.Error(w, "method", 405)
		return true
	}
	rt, e := r.route(q.Context(), id)
	if e != nil {
		http.NotFound(w, q)
		return true
	}
	owner := coordinator.Identity
	ep, tc, check, e := r.peer(q.Context(), owner, DeliveryPath)
	if e != nil {
		http.Error(w, "delivery unavailable", 502)
		return true
	}
	transport, e := outbound.New(ep, tc, 5*time.Second, check)
	if e != nil {
		http.Error(w, "delivery unavailable", 502)
		return true
	}
	defer transport.CloseIdleConnections()
	request, e := http.NewRequestWithContext(q.Context(), q.Method, ep, nil)
	if e != nil {
		http.Error(w, "delivery unavailable", 502)
		return true
	}
	if err := copyDeliveryHeaders(request.Header, q.Header); err != nil {
		http.Error(w, "request headers too large", 431)
		return true
	}

	origin, e := captureOrigin(q)
	if e != nil {
		http.Error(w, "invalid request origin", 400)
		return true
	}
	request.Header.Set(deliveryOrigin, origin)
	request.Header.Set(deliveryHop, "1")
	request.Header.Set(deliveryURI, q.URL.RequestURI())
	request.Header.Set(deliveryProtocol, protocol)
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Do(request)
	if e != nil {
		http.Error(w, "delivery unavailable", 502)
		return true
	}
	defer response.Body.Close()
	for _, h := range deliveryHeaders {
		if values := response.Header.Values(h); len(values) > 0 {
			w.Header()[h] = values
		}
	}
	// Redirects cannot reveal private host addresses, even if a callback is wrong.
	if response.StatusCode >= 300 && response.StatusCode < 400 && response.StatusCode != 304 {
		http.Error(w, "delivery unavailable", 502)
		return true
	}
	w = r.fencedDelivery(w, q, rt)
	w.WriteHeader(response.StatusCode)
	if q.Method != "HEAD" {
		_, _ = io.Copy(w, response.Body)
	}
	return true
}

var _ = context.Background

// Generation zero is the preparation-only direct-play mapping; once an encoder
// is assigned, placement and the durable route must agree on its complete fence.
// Moving its exact source preserves encoder output authority: SourceGeneration,
// source node and PlayID belong to input fencing rather than delivery fencing.
func (r *Runtime) deliveryAuthority(ctx context.Context, expected Route) error {
	current, err := r.route(ctx, expected.Fence.Session)
	if err != nil || current.Fence != expected.Fence || current.Viewer != expected.Viewer || current.Revision != expected.Revision || (expected.Fence.Generation == 0 && current != expected) {
		return ErrDenied
	}
	if expected.Fence.Generation > 0 {
		record, err := r.o.Placement.Lookup(ctx, expected.Fence.Session)
		if err != nil || record.Fence != expected.Fence || !record.Authority || record.Retired || !record.Deadline.Add(-r.o.ClockSkew).After(time.Now()) {
			return ErrDenied
		}
	}
	return nil
}
func (r *Runtime) fencedDelivery(w http.ResponseWriter, q *http.Request, route Route) http.ResponseWriter {
	return &generationfence.Writer{ResponseWriter: w, Check: func() error { return r.deliveryAuthority(q.Context(), route) }}
}

// Copy only protocol credential/device and representation headers. Keep the
// original multiplicity and values so frontend precedence remains unchanged.
func copyDeliveryHeaders(dst, src http.Header) error {
	names := []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since", "Accept", "User-Agent", "Authorization", "X-Emby-Authorization", "X-Emby-Token", "X-MediaBrowser-Token", "X-Emby-Device-Id", "X-Emby-Device-Name", "X-Emby-Client", "X-Emby-Client-Version", "Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"}
	total := 0
	for _, name := range names {
		values := src.Values(name)
		if len(values) > 16 {
			return ErrDenied
		}
		for _, value := range values {
			total += len(name) + len(value)
			if len(value) > 4096 || total > 16384 {
				return ErrDenied
			}
		}
		dst[name] = append([]string(nil), values...)
	}
	return nil
}
