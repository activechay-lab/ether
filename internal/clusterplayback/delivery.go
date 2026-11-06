package clusterplayback

import (
	"context"
	"github.com/braedonsaunders/ether/internal/generationfence"
	"github.com/braedonsaunders/ether/internal/nodewire"
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
	rt, e := r.route(q.Context(), parts[0])
	if e != nil || rt.Fence.Controller.ID != r.o.Self.NodeID || rt.Fence.Controller.Boot != r.o.Self.Boot {
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
	guarded := r.fencedDelivery(w, q, rt)
	defer guarded.Close()
	handler(guarded, clone, q.Header.Get(deliveryProtocol))
}

// ForwardMedia performs at most one indexed ownership hop. No redirect or private
// endpoint is returned to a client. The owning broker authenticates the grant.
func (r *Runtime) ForwardMedia(w http.ResponseWriter, q *http.Request, id, protocol string) bool {
	if q.Header.Get(deliveryHop) != "" {
		return false
	}
	rt, e := r.route(q.Context(), id)
	if e != nil || rt.Fence.Controller.ID == r.o.Self.NodeID && rt.Fence.Controller.Boot == r.o.Self.Boot {
		return false
	}
	if q.Method != "GET" && q.Method != "HEAD" {
		http.Error(w, "method", 405)
		return true
	}
	owner := nodewire.Identity{Cluster: r.o.Self.Cluster, NodeID: rt.Fence.Controller.ID, Boot: rt.Fence.Controller.Boot}
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
	guarded := r.fencedDelivery(w, q, rt)
	defer guarded.Close()
	w = guarded
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
	_, err := r.deliveryAuthorityDeadline(ctx, expected)
	return err
}
func (r *Runtime) deliveryAuthorityDeadline(ctx context.Context, expected Route) (time.Time, error) {
	current, err := r.route(ctx, expected.Fence.Session)
	if err != nil || current.Fence != expected.Fence || current.Viewer != expected.Viewer || current.Revision != expected.Revision || (expected.Fence.Generation == 0 && current != expected) {
		return time.Time{}, ErrDenied
	}
	if expected.Fence.Generation > 0 {
		record, err := r.o.Placement.Lookup(ctx, expected.Fence.Session)
		if err != nil || record.Fence != expected.Fence || !record.Authority || record.Retired || !record.Deadline.Add(-r.o.ClockSkew).After(time.Now()) {
			return time.Time{}, ErrDenied
		}
		return record.Deadline.Add(-r.o.ClockSkew), nil
	}
	return time.Time{}, nil
}
func (r *Runtime) fencedDelivery(w http.ResponseWriter, q *http.Request, route Route) *generationfence.Writer {
	var deadline time.Time
	return &generationfence.Writer{ResponseWriter: w, Watch: func(fn func()) func() { return r.watchAuthority(route.Fence.Session, fn) }, LocalCheck: func() error {
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return ErrDenied
		}
		if q.Context().Err() != nil {
			return q.Context().Err()
		}
		r.mu.Lock()
		closed := r.closed
		r.mu.Unlock()
		if closed {
			return ErrDenied
		}
		return nil
	}, Check: func() error {
		until, err := r.deliveryAuthorityDeadline(q.Context(), route)
		if err == nil && !until.IsZero() {
			now := time.Now()
			deadline = now.Add(until.Sub(now))
		}
		return err
	}}
}
