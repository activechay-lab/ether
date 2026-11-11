package frontendadapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// Auth modes an adapter declaration may pick (spec §6.5).
const (
	AuthEtherToken = "ether_token"
	AuthBasic      = "basic"
	AuthNoneLAN    = "none_lan"
)

// errNotVisible means the authenticated viewer cannot see the requested
// library. The proxy answers 404, never 403, so a hidden library's
// existence does not leak.
var errNotVisible = errors.New("frontendadapter: library not visible")

// errUnauthenticated means no valid viewer credential was presented.
var errUnauthenticated = errors.New("frontendadapter: unauthenticated")

// authenticated is the result of authenticating one inbound request.
type authenticated struct {
	viewer *core.Viewer
	// libraries is the intersection of the adapter's declared libraries
	// and the viewer's visible libraries.
	libraries []core.LibraryID
}

// authenticator resolves the viewer for one adapter from the host identity
// surface, then filters the adapter's libraries to those the viewer sees.
type authenticator struct {
	decl Declaration
	host frontend.Host
}

func newAuthenticator(decl Declaration, host frontend.Host) (*authenticator, error) {
	switch decl.Meta.Auth {
	case "", AuthEtherToken, AuthBasic, AuthNoneLAN:
	default:
		return nil, fmt.Errorf("frontendadapter: adapter %q has unknown auth mode %q", decl.Meta.Key, decl.Meta.Auth)
	}
	return &authenticator{decl: decl, host: host}, nil
}

// authenticate resolves the viewer and their visible libraries. The
// returned error is errUnauthenticated or errNotVisible; the caller maps
// those to 401 and 404.
func (a *authenticator) authenticate(ctx context.Context, r *http.Request) (authenticated, error) {
	switch a.decl.Meta.Auth {
	case AuthNoneLAN:
		return a.authenticateLAN(ctx, r)
	case AuthBasic:
		return a.authenticateBasic(ctx, r)
	default:
		return a.authenticateToken(ctx, r)
	}
}

// authenticateToken resolves an Ether credential (a bearer token, or the
// conventional Basic password an OPDS reader supplies) through the bound
// identity surface for this adapter's protocol.
func (a *authenticator) authenticateToken(ctx context.Context, r *http.Request) (authenticated, error) {
	credential := bearerCredential(r)
	if credential == "" {
		if user, pass, ok := r.BasicAuth(); ok {
			credential = pass
			if credential == "" {
				credential = user
			}
		}
	}
	if credential == "" {
		return authenticated{}, errUnauthenticated
	}
	return a.resolve(ctx, r, credential)
}

// authenticateBasic resolves the credential Ether issued for this adapter
// via HTTP Basic. Ether still owns the credential; the plugin does not.
func (a *authenticator) authenticateBasic(ctx context.Context, r *http.Request) (authenticated, error) {
	user, pass, ok := r.BasicAuth()
	if !ok {
		return authenticated{}, errUnauthenticated
	}
	credential := pass
	if credential == "" {
		credential = user
	}
	return a.resolve(ctx, r, credential)
}

// authenticateLAN follows the DLNA rule: only a request Ether has already
// classified as coming from a trusted LAN, with an owner-picked viewer,
// is accepted without a credential.
func (a *authenticator) authenticateLAN(ctx context.Context, r *http.Request) (authenticated, error) {
	if a.host.LANViewer == nil {
		return authenticated{}, errUnauthenticated
	}
	viewer, err := a.host.LANViewer(ctx, r)
	if err != nil || viewer == nil {
		return authenticated{}, errUnauthenticated
	}
	return a.visible(viewer)
}

// resolve resolves a credential for the adapter's protocol and filters the
// libraries.
func (a *authenticator) resolve(ctx context.Context, r *http.Request, credential string) (authenticated, error) {
	if a.host.Identity == nil {
		return authenticated{}, errUnauthenticated
	}
	// A quiet identity skips activity bookkeeping; proxy requests are
	// frequent and should not each write viewer-times rows.
	viewer, err := a.host.Identity.Resolve(frontend.WithQuietIdentity(ctx), credential)
	if err != nil || viewer == nil {
		return authenticated{}, errUnauthenticated
	}
	return a.visible(viewer)
}

// visible intersects the adapter's declared libraries with the viewer's
// visible libraries. A viewer who can see none of them is answered 404.
func (a *authenticator) visible(viewer *core.Viewer) (authenticated, error) {
	allowed := make([]core.LibraryID, 0, len(a.decl.Libraries))
	for _, id := range a.decl.Libraries {
		if viewer.AllowsLibrary(id) {
			allowed = append(allowed, id)
		}
	}
	if len(a.decl.Libraries) > 0 && len(allowed) == 0 {
		return authenticated{}, errNotVisible
	}
	return authenticated{viewer: viewer, libraries: allowed}, nil
}

// bearerCredential reads a bearer token from the Authorization header.
func bearerCredential(r *http.Request) string {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
		return strings.TrimSpace(header[len(prefix):])
	}
	return ""
}
