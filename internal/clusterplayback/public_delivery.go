package clusterplayback

import (
	"context"
	"github.com/braedonsaunders/ether/internal/config"
	"net/http"
	"strings"
)

// NewPublicDelivery constructs the delivery-only public representation sender.
// AuthorizePlex must perform the same live valid-token/receipt checks as the
// owning Plex frontend. Grant-only media uses the separate canonical envelope and owner authentication.
// Neutral grant authentication and current viewer/media policy run at owner.
func (r *Runtime) NewPublicDelivery(authorizePlex func(context.Context, *http.Request, string) error) (http.Handler, error) {
	if authorizePlex == nil || r.o.Roles.Has(config.RoleServer) || !r.o.Roles.Has(config.RoleDelivery) {
		return nil, ErrDenied
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if q.Method != "GET" && q.Method != "HEAD" {
			http.Error(w, "method", 405)
			return
		}
		if q.Header.Get(deliveryHop) != "" || q.URL.RawPath != "" {
			http.NotFound(w, q)
			return
		}
		if path, id, protocol, ok := neutralDeliveryRoute(q.URL.Path); ok {
			clone := q.Clone(q.Context())
			clone.URL.Path = path
			clone.RequestURI = clone.URL.RequestURI()
			if !r.ForwardMedia(w, clone, id, protocol) {
				http.NotFound(w, q)
			}
			return
		}
		if id, ok := plexMediaSession(q.URL); ok {
			if plexGrantEnvelope(q) {
				if !r.ForwardPlexGrantMedia(w, q, id) {
					http.NotFound(w, q)
				}
				return
			}
			if authorizePlex(q.Context(), q, id) != nil {
				http.Error(w, "unauthorized", 401)
				return
			}
			if !r.ForwardPlexMedia(w, q, id) {
				http.NotFound(w, q)
			}
			return
		}
		http.NotFound(w, q)
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.publicDelivery {
		return nil, ErrDenied
	}
	r.publicDelivery = true
	return handler, nil
}
func neutralMediaSession(path string) (string, bool) {
	const prefix = "/sessions/media/"
	if len(path) > 8192 || !strings.HasPrefix(path, prefix) {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) < 3 || len(parts) > 8 {
		return "", false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || len(part) > 512 {
			return "", false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
				return "", false
			}
		}
	}
	return parts[0], true
}

func neutralDeliveryRoute(path string) (canonical, id, protocol string, ok bool) {
	canonical = path
	switch {
	case strings.HasPrefix(path, "/jellyfin/sessions/media/"):
		canonical = strings.TrimPrefix(path, "/jellyfin")
		protocol = "jellyfin"
	case strings.HasPrefix(path, "/emby/sessions/media/"):
		canonical = strings.TrimPrefix(path, "/emby")
		protocol = "emby"
	}
	id, ok = neutralMediaSession(canonical)
	return
}
