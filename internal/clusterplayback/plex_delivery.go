package clusterplayback

import (
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/nodewire/outbound"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const PlexDeliveryPath = "/cluster/playback/plex-media"

// BindPlexDelivery must receive the original authenticated public Plex handler,
// including existing token/grant/policy middleware, before listeners start.
func (r *Runtime) BindPlexDelivery(handler http.Handler) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if handler == nil || r.plexDelivery != nil {
		return ErrDenied
	}
	r.plexDelivery = handler
	return nil
}
func plexMediaSession(u *url.URL) (string, bool) {
	const prefix = "/video/:/transcode/universal/session/"
	if !strings.HasPrefix(u.Path, prefix) {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, prefix), "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", false
	}
	for _, part := range parts {
		if part == "." || part == ".." || len(part) > 256 {
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

var plexForwardHeaders = []string{"Authorization", "X-Plex-Token", "X-Plex-Client-Identifier", "X-Plex-Product", "X-Plex-Version", "X-Plex-Platform", "X-Plex-Device", "X-Plex-Device-Name", "X-Plex-Session-Identifier", "Range", "If-Range", "If-None-Match", "If-Modified-Since"}

func (r *Runtime) plexDeliveryHTTP(w http.ResponseWriter, q *http.Request) {
	peer, e := r.o.Guard.Request(q)
	if e != nil || r.o.MemberRole(q.Context(), peer, config.RoleDelivery) != nil {
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
	if len(raw) > 16384 {
		http.Error(w, "scope", 400)
		return
	}
	u, e := url.ParseRequestURI(raw)
	if e != nil || u.IsAbs() || u.Host != "" || u.RawPath != "" || u.Fragment != "" {
		http.Error(w, "scope", 400)
		return
	}
	id, ok := plexMediaSession(u)
	if !ok {
		http.NotFound(w, q)
		return
	}
	route, e := r.route(q.Context(), id)
	if e != nil || route.Fence.Controller.ID != r.o.Self.NodeID || route.Fence.Controller.Boot != r.o.Self.Boot {
		http.NotFound(w, q)
		return
	}
	r.mu.Lock()
	handler := r.plexDelivery
	r.mu.Unlock()
	if handler == nil {
		http.Error(w, "unavailable", 503)
		return
	}
	clone := q.Clone(q.Context())
	clone.URL = u
	clone.RequestURI = raw
	handler.ServeHTTP(w, clone)
}

// ForwardPlexMedia forwards only validated representation paths. The receiver
// runs the original Plex token/grant checks. No control/start endpoint is eligible.
func (r *Runtime) ForwardPlexMedia(w http.ResponseWriter, q *http.Request, publicID string) bool {
	if q.Header.Get(deliveryHop) != "" {
		return false
	}
	id, ok := plexMediaSession(q.URL)
	if !ok || id != publicID {
		return false
	}
	if q.Method != "GET" && q.Method != "HEAD" {
		return false
	}
	route, e := r.route(q.Context(), id)
	if e != nil || route.Fence.Controller.ID == r.o.Self.NodeID && route.Fence.Controller.Boot == r.o.Self.Boot {
		return false
	}
	owner := nodewire.Identity{Cluster: r.o.Self.Cluster, NodeID: route.Fence.Controller.ID, Boot: route.Fence.Controller.Boot}
	ep, tc, check, e := r.peer(q.Context(), owner, PlexDeliveryPath)
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
	for _, h := range plexForwardHeaders {
		request.Header[h] = append([]string(nil), q.Header.Values(h)...)
	}
	request.Header.Set(deliveryHop, "1")
	request.Header.Set(deliveryURI, q.URL.RequestURI())
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Do(request)
	if e != nil {
		http.Error(w, "delivery unavailable", 502)
		return true
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 && response.StatusCode != 304 {
		http.Error(w, "delivery unavailable", 502)
		return true
	}
	for _, h := range deliveryHeaders {
		if values := response.Header.Values(h); len(values) > 0 {
			w.Header()[h] = values
		}
	}
	w.WriteHeader(response.StatusCode)
	if q.Method != "HEAD" {
		_, _ = io.Copy(w, response.Body)
	}
	return true
}
