package clusterplayback

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/braedonsaunders/ether/internal/core"
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
	guarded := r.fencedPublicDelivery(w, q, rt)
	defer guarded.Close()
	if pathProtocol != "" {
		mediaBrowser.ServeHTTP(guarded, clone)
		return
	}
	handler(guarded, clone, protocol)
}

// ForwardMedia performs at most one indexed ownership hop. No redirect or private
// endpoint is returned to a client. The owning broker authenticates the grant.
func (r *Runtime) ForwardMedia(w http.ResponseWriter, q *http.Request, id, protocol string) bool {
	if q.Header.Get(deliveryHop) != "" {
		return false
	}
	owner, e := r.mediaServer(q.Context(), id)
	if e != nil || owner == r.o.Self {
		return false
	}
	rt, e := r.route(q.Context(), id)
	if e != nil {
		http.NotFound(w, q)
		return true
	}
	ep, tc, check, e := r.peer(q.Context(), owner, DeliveryPath)
	if e != nil {
		http.Error(w, "delivery unavailable", 502)
		return true
	}
	transport, e := outbound.New(ep, tc, 500*time.Millisecond, check)
	if e != nil {
		http.Error(w, "delivery unavailable", 502)
		return true
	}
	transport.ResponseHeaderTimeout(r.o.EncoderSegmentWait)
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
	client := &http.Client{Transport: &mediaFailoverTransport{runtime: r, first: transport, owner: owner, path: DeliveryPath}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
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
	guarded := r.fencedPublicDelivery(w, q, rt)
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
		if r.o.Self.Cluster != "" {
			h, err := r.readOperationGate(ctx, operationKey{r.o.Self.Cluster, expected.Fence.Session, expected.Fence.Generation})
			// An operation gate records distributed execution cancellation; it
			// is not a prerequisite for independently authorized delivery. Local
			// frontend routes can have a placement fence without that history.
			// Absence is not cancellation. Unreadable/corrupt history remains
			// uncertain, while only an explicit cancellation seal revokes media.
			if err != nil && !errors.Is(err, sql.ErrNoRows) || err == nil && h.Gate.Seal != nil && h.Gate.Seal.Reason == sealCancellation {
				return time.Time{}, ErrDenied
			}
		}
		record, err := r.o.Placement.Lookup(ctx, expected.Fence.Session)
		if err != nil || record.Fence != expected.Fence || !record.Authority || record.Retired || !record.Deadline.Add(-r.o.ClockSkew).After(time.Now()) {
			return time.Time{}, ErrDenied
		}
		return record.Deadline.Add(-r.o.ClockSkew), nil
	}
	return time.Time{}, nil
}
func (r *Runtime) fencedDelivery(w http.ResponseWriter, q *http.Request, route Route) *generationfence.Writer {
	return r.fencedDeliveryResolved(w, q, route, nil)
}

// Resolve at first output, after any encoder wait. A successful durable check
// pins the response; revalidation never changes its generation mid-body.
func (r *Runtime) fencedDeliveryResolved(w http.ResponseWriter, q *http.Request, route Route, resolve func() (Route, error)) *generationfence.Writer {
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
		if resolve != nil {
			current, err := resolve()
			if err != nil {
				return err
			}
			route = current
		}
		until, err := r.deliveryAuthorityDeadline(q.Context(), route)
		if err == nil {
			resolve = nil
		}
		if err == nil && !until.IsZero() {
			now := time.Now()
			deadline = now.Add(until.Sub(now))
		}
		return err
	}}
}

// Only the stable recovery session may cross a recorded encoder replacement
// before headers. Raw worker and ordinary delivery writers remain exact-fenced.
func (r *Runtime) fencedPublicDelivery(w http.ResponseWriter, q *http.Request, expected Route) *generationfence.Writer {
	return r.fencedDeliveryResolved(w, q, expected, func() (Route, error) {
		current, err := r.route(q.Context(), expected.Fence.Session)
		if err != nil {
			return Route{}, err
		}
		if current.Fence == expected.Fence {
			return expected, nil
		}
		if expected.Fence.Generation == 0 || current.Viewer != expected.Viewer || current.Revision != expected.Revision || current.Fence.Controller != expected.Fence.Controller {
			return Route{}, ErrDenied
		}
		if current.Fence.Generation <= expected.Fence.Generation || current.Fence.Generation-expected.Fence.Generation > int64(r.o.HistoryCapacity) {
			return Route{}, ErrDenied
		}
		// Delivery replicas have no local execution map. Prove the exact sequence
		// of committed recovery transitions in one indexed durable read instead.
		rows, err := r.o.DB.QueryContext(q.Context(), `SELECT decision FROM clusterplayback_recovery_decisions WHERE cluster=? AND session=? AND generation>? AND generation<=? ORDER BY generation`, r.o.Self.Cluster, expected.Fence.Session, expected.Fence.Generation, current.Fence.Generation)
		if err != nil {
			return Route{}, err
		}
		defer rows.Close()
		fence := expected.Fence
		for rows.Next() {
			var raw string
			var decision struct {
				Previous, Next Route
				Request        core.HLSRequest
				Keyframes      []core.KeyframePoint
			}
			if rows.Scan(&raw) != nil || len(raw) > maxAdmissionBytes || json.Unmarshal([]byte(raw), &decision) != nil {
				return Route{}, ErrDenied
			}
			decision.Request.Info.Keyframes = decision.Keyframes
			if decision.Previous.Fence != fence || decision.Next.Fence.Generation != fence.Generation+1 || decision.Next.Fence.Session != fence.Session || decision.Next.Fence.Controller != fence.Controller || decision.Next.Viewer != expected.Viewer || decision.Next.Revision != expected.Revision || !encoderRecoverySupported(decision.Request, decision.Next.Encoder) || decision.Request.Resume == nil || decision.Request.Resume.Generation != uint64(decision.Next.Fence.Generation) {
				return Route{}, ErrDenied
			}
			fence = decision.Next.Fence
		}
		if err = rows.Err(); err != nil {
			return Route{}, err
		}
		if fence != current.Fence {
			return Route{}, ErrDenied
		}
		return current, nil
	})
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
