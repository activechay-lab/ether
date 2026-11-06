package clusterplayback

import (
	"context"
	"github.com/braedonsaunders/ether/internal/nodewire/outbound"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DeliveryPath = "/cluster/playback/delivery"
const deliveryHop = "X-Ether-Delivery-Hop"
const deliveryURI = "X-Ether-Delivery-URI"
const deliveryProtocol = "X-Ether-Delivery-Protocol"

var deliveryHeaders = []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Cache-Control", "ETag", "Last-Modified", "Content-Disposition"}

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
	if q.Method != "GET" && q.Method != "HEAD" {
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
	if e != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/sessions/media/") {
		http.Error(w, "scope", 400)
		return
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/sessions/media/"), "/")
	if len(parts) < 3 || parts[0] == "" {
		http.NotFound(w, q)
		return
	}
	coordinator, e := r.CurrentCoordinator(q.Context(), parts[0])
	if e != nil || coordinator.Identity != r.o.Self {
		http.NotFound(w, q)
		return
	}
	r.mu.Lock()
	handler := r.delivery
	r.mu.Unlock()
	if handler == nil {
		http.Error(w, "unavailable", 503)
		return
	}
	clone, e := restoreOrigin(q, u, raw)
	if e != nil {
		http.Error(w, "scope", 400)
		return
	}
	handler(w, clone, q.Header.Get(deliveryProtocol))
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
	if q.Method != "GET" && q.Method != "HEAD" {
		http.Error(w, "method", 405)
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
	for _, h := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		request.Header[h] = append([]string(nil), q.Header.Values(h)...)
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
	w.WriteHeader(response.StatusCode)
	if q.Method != "HEAD" {
		_, _ = io.Copy(w, response.Body)
	}
	return true
}

var _ = context.Background
