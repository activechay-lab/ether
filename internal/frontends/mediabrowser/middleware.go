package mediabrowser

import (
	"context"
	"net/http"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/authhdr"
	"github.com/braedonsaunders/ether/internal/frontend/kit/route"
	"github.com/braedonsaunders/ether/internal/frontend/kit/support"
)

// anonymousStreamKey marks a request the middleware admitted without a
// credential so the handler can serve it from a recent session.
type anonymousStreamKey struct{}

// These exact method/pattern pairs are pre-login endpoints. Jellyfin's
// System/Endpoint requires authentication, but Emby clients use Ether's
// existing anonymous response before sign-in (see public_test.go).
// Media and image routes instead verify their own unguessable scoped grants.
var publicRoutesAllowlist = map[route.Route]bool{
	{Method: "GET", Pattern: "/Branding/Css"}:                               true,
	{Method: "GET", Pattern: "/Branding/Css.css"}:                           true,
	{Method: "GET", Pattern: "/System/Info/Public"}:                         true,
	{Method: "GET", Pattern: "/System/Endpoint"}:                            true,
	{Method: "GET", Pattern: "/System/Ping"}:                                true,
	{Method: "POST", Pattern: "/System/Ping"}:                               true,
	{Method: "GET", Pattern: "/Branding/Configuration"}:                     true,
	{Method: "GET", Pattern: "/web"}:                                        true,
	{Method: "GET", Pattern: "/web/{path...}"}:                              true,
	{Method: "GET", Pattern: "/Users/Public"}:                               true,
	{Method: "POST", Pattern: "/Users/AuthenticateByName"}:                  true,
	{Method: "POST", Pattern: "/Users/AuthenticateWithQuickConnect"}:        true,
	{Method: "GET", Pattern: "/QuickConnect/Enabled"}:                       true,
	{Method: "POST", Pattern: "/QuickConnect/Initiate"}:                     true,
	{Method: "GET", Pattern: "/QuickConnect/Connect"}:                       true,
	{Method: "GET", Pattern: "/GetUtcTime"}:                                 true,
	{Method: "GET", Pattern: "/Items/{itemId}/Images/{imageType}"}:          true,
	{Method: "HEAD", Pattern: "/Items/{itemId}/Images/{imageType}"}:         true,
	{Method: "GET", Pattern: "/Items/{itemId}/Images/{imageType}/{index}"}:  true,
	{Method: "HEAD", Pattern: "/Items/{itemId}/Images/{imageType}/{index}"}: true,
	{Method: "GET", Pattern: "/sessions/lazy/{path...}"}:                    true,
	{Method: "HEAD", Pattern: "/sessions/lazy/{path...}"}:                   true,
	{Method: "GET", Pattern: "/sessions/media/{path...}"}:                   true,
	{Method: "HEAD", Pattern: "/sessions/media/{path...}"}:                  true,
}

// anonymousStreamRoutes are the media stream aliases a client may request
// without any credential. Real Jellyfin serves these anonymously
// (VideosController.GetVideoStream has no [Authorize]); Findroid and the
// Jellyfin Kotlin SDK build /Videos/{id}/stream URLs and hand them to
// ExoPlayer with no token. The handler admits such a request only when it
// matches a playback session an authenticated viewer just opened from the
// same client IP for the same item (see anonIndex). An authenticated
// request on these routes still flows through the normal path.
var anonymousStreamRoutes = map[route.Route]bool{
	{Method: "GET", Pattern: "/Videos/{id}/{name}"}:  true,
	{Method: "HEAD", Pattern: "/Videos/{id}/{name}"}: true,
	{Method: "GET", Pattern: "/Audio/{id}/{name}"}:   true,
	{Method: "HEAD", Pattern: "/Audio/{id}/{name}"}:  true,
}

func (i *instance) routeMiddleware(rt route.Route, next http.Handler) http.Handler {
	if i.pages != nil && i.host.PageFreshness != nil {
		original := next
		cached := i.pages.wrap(next, i.host.PageFreshness)
		next = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !publicRoutesAllowlist[rt] {
				if _, _, err := i.viewer(r); err != nil {
					original.ServeHTTP(w, r)
					return
				}
			}
			cached.ServeHTTP(w, r)
		})
	}
	next = support.Wrap(i.host.Support, string(i.protocol), func(r *http.Request) (*frontend.Viewer, frontend.Device) {
		if _, ok := i.host.Identity.(frontend.GrantDeviceIdentity); !ok {
			return nil, frontend.Device{}
		}
		v, _, err := i.viewer(r)
		if err != nil {
			return nil, frontend.Device{}
		}
		return v, device(r)
	}, func(path string) string { return path }, next)
	if publicRoutesAllowlist[rt] {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, token, err := i.authenticate(r)
		if err != nil || v == nil {
			// A stream alias with no credential at all is answered by the
			// handler against a just-opened session; anything carrying a bad
			// or foreign credential is still rejected here.
			if anonymousStreamRoutes[rt] && !authhdr.MediaBrowserCredentialSupplied(r) {
				r = r.WithContext(context.WithValue(r.Context(), anonymousStreamKey{}, true))
				next.ServeHTTP(w, r)
				return
			}
			if i.protocol == "emby" && anonymousStreamRoutes[rt] && queryBool(r, "Static", false) {
				name := strings.ToLower(r.PathValue("name"))
				if name == "stream" || strings.HasPrefix(name, "stream.") {
					w = &embyStaticResponse{ResponseWriter: w}
				}
			}
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), principalKey{}, principal{viewer: v, token: token}))
		if _, _, err := i.viewer(r); err == nil {
			if i.host.ClientSeen != nil {
				i.host.ClientSeen(device(r).ID)
			}
			if _, ok := i.host.Identity.(frontend.GrantDeviceIdentity); ok {
				support.Seen(r, i.host.Support, string(i.protocol), v, device(r))
			}
		}
		next.ServeHTTP(w, r)
	})
}
