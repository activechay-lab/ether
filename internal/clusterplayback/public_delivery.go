package clusterplayback

import (
	"context"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"net/http"
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
		if q.Method != "GET" && q.Method != "HEAD" && !mediaBrowserPreflight(q, r.o.Delivery) {
			http.Error(w, "method", 405)
			return
		}
		if !IsPublicRepresentation(q, r.o.Delivery) || q.Header.Get(deliveryHop) != "" {
			http.NotFound(w, q)
			return
		}
		if _, id, protocol, ok := r.o.Delivery.Route(q.URL.Path); ok {
			if !r.ForwardMedia(w, q, id, protocol) {
				http.NotFound(w, q)
			}
			return
		}

		if id, ok := r.o.Delivery.Session(q.URL); ok {
			if r.o.Delivery.GrantEnvelope(q) {
				if !r.ForwardGrantMedia(w, q, id) {
					http.NotFound(w, q)
				}
				return
			}
			if authorizePlex(q.Context(), q, id) != nil {
				http.Error(w, "unauthorized", 401)
				return
			}
			if !r.ForwardEncodedMedia(w, q, id) {
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

// IsPublicRepresentation reports only the supported delivery route shape. It
// grants no access and says nothing about listener/readiness or backend support.
func IsPublicRepresentation(q *http.Request, policies ...core.DeliveryPolicy) bool {
	if q == nil || q.URL == nil || (q.Method != "GET" && q.Method != "HEAD" && !mediaBrowserPreflight(q, policies...)) || q.URL.RawPath != "" || q.URL.Fragment != "" || q.URL.Opaque != "" {
		return false
	}
	if len(policies) > 0 && policies[0].Route != nil {
		if _, _, _, ok := policies[0].Route(q.URL.Path); ok {
			return len(q.URL.RequestURI()) <= 8192
		}
	}
	if len(policies) > 0 && policies[0].Session != nil {
		if _, ok := policies[0].Session(q.URL); ok {
			return len(q.URL.RequestURI()) <= 16384
		}
	}
	return false
}

// MediaBrowser frontends own their ordinary OPTIONS/CORS response. Only a
// typed alias can carry a preflight to that original public handler.
func mediaBrowserPreflight(q *http.Request, policies ...core.DeliveryPolicy) bool {
	if q == nil || q.URL == nil || q.Method != "OPTIONS" {
		return false
	}
	if len(policies) == 0 || policies[0].Route == nil {
		return false
	}
	_, _, protocol, ok := policies[0].Route(q.URL.Path)
	return ok && protocol != ""
}
