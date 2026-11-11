// Package frontendadapter registers one generic Ether frontend per
// plugin-provided protocol surface (spec §6.5). A plugin cannot register Go
// code, so Ether mounts a reverse proxy that turns each HTTP request into a
// plugins.Manager.CallStream call, authenticating the viewer through Ether
// first and injecting only the viewer-identity headers the spec defines. The
// plugin never sees an Ether credential.
package frontendadapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// AdapterID builds the frontend protocol id for one adapter declaration:
// "plugin:<pluginID>:<adapterKey>". It is stable across mounts so an
// owner's Apps-page settings survive enable/disable cycles.
func AdapterID(pluginID, adapterKey string) frontend.Protocol {
	return frontend.Protocol("plugin:" + pluginID + ":" + adapterKey)
}

// Declaration is everything the generic frontend needs from one
// contributes.frontendAdapters[] entry plus its owning plugin.
type Declaration struct {
	PluginID string
	Meta     plugins.FrontendAdapterMeta
	// Name and Icon come from the plugin manifest (spec §6.5). Name is the
	// adapter label; Icon is the plugin logo when the adapter has none.
	Name string
	Icon string
	// DocsURL and Version are surfaced on the descriptor for the Apps page.
	DocsURL string
	Version string
	// SettingsSchema is the adapter's (or the plugin's) settings schema, if
	// any, forwarded verbatim to the descriptor.
	SettingsSchema any
	// Libraries lists the library ids this adapter publishes. The proxy
	// intersects them with the viewer's visible libraries: a request that
	// would expose a library the viewer cannot see answers 404, never 403.
	// Empty means the adapter is not library-scoped (the viewer still must
	// authenticate) and X-Ether-Libraries is empty.
	Libraries []core.LibraryID
}

// Defaults for the isolation limits in spec §6.5.
const (
	// DefaultRequestsPerMinute is the per-adapter, per-viewer token-bucket
	// budget when a declaration does not override it.
	DefaultRequestsPerMinute = 600
	// MaxRequestBodyBytes caps an inbound proxied body.
	MaxRequestBodyBytes = 1 << 20
	// MaxHostedResponseBytes caps a hosted (wasm) plugin's response body.
	// The hosted runtime answers small protocol documents through Call;
	// media bytes belong on Ether stream URLs instead.
	MaxHostedResponseBytes = 10 << 20
)

// Frontend is the frontend.Frontend implementation for one adapter. It is
// registered on plugin enable and unregistered on disable/remove.
type Frontend struct {
	decl Declaration
	mgr  Manager
}

var _ frontend.Frontend = (*Frontend)(nil)

// New builds the generic frontend for one adapter declaration.
func New(decl Declaration, mgr Manager) (*Frontend, error) {
	if decl.PluginID == "" {
		return nil, errors.New("frontendadapter: empty plugin id")
	}
	if decl.Meta.Key == "" {
		return nil, errors.New("frontendadapter: empty adapter key")
	}
	if mgr == nil {
		return nil, errors.New("frontendadapter: nil manager")
	}
	if !strings.HasPrefix(decl.Meta.PathPrefix, "/") || decl.Meta.PathPrefix == "/" {
		return nil, fmt.Errorf("frontendadapter: adapter %q has no usable path prefix", decl.Meta.Key)
	}
	return &Frontend{decl: decl, mgr: mgr}, nil
}

// ID returns the frontend protocol id.
func (f *Frontend) ID() frontend.Protocol { return AdapterID(f.decl.PluginID, f.decl.Meta.Key) }

// Describe reports the descriptor Ether's Apps page renders (spec §6.5).
func (f *Frontend) Describe() frontend.Descriptor {
	port := f.decl.Meta.DefaultPort
	name := strings.TrimSpace(f.decl.Name)
	if name == "" {
		name = f.decl.Meta.Label
	}
	return frontend.Descriptor{
		ID:             f.ID(),
		Name:           name,
		Logo:           f.decl.Icon,
		Version:        f.decl.Version,
		DocsURL:        f.decl.DocsURL,
		DefaultPort:    port,
		Clients:        append([]string(nil), f.decl.Meta.Clients...),
		SettingsSchema: f.decl.SettingsSchema,
	}
}

// Mount binds the adapter to the shared listener under its path prefix (or
// its own port when the owner enabled defaultPort) and returns the proxy.
func (f *Frontend) Mount(ctx context.Context, host frontend.Host) (frontend.Instance, error) {
	if f.decl.Meta.Auth == AuthNoneLAN {
		if host.LANViewer == nil {
			return nil, errors.New("frontendadapter: none_lan adapter needs a LAN viewer resolver")
		}
	} else if host.Identity == nil {
		return nil, errors.New("frontendadapter: host has no identity surface")
	}
	auth, err := newAuthenticator(f.decl, host)
	if err != nil {
		return nil, err
	}
	inst := &instance{
		frontend: f,
		auth:     auth,
		limits:   newLimiters(f.decl),
	}
	inst.handler = f.proxyHandler(inst)
	return inst, nil
}

// Manager is the narrow slice of plugins.Manager the proxy uses. It is an
// interface so tests can supply a fake and so the adapter cannot reach any
// other Manager method. CallStream relays bodies (remote/tunnel plugins);
// Call serves small buffered responses and is the hosted-plugin fallback.
type Manager interface {
	CallStream(ctx context.Context, pluginID string, req plugins.Request) (plugins.Response, io.ReadCloser, error)
	Call(ctx context.Context, pluginID string, req plugins.Request) (plugins.Response, error)
}

// pluginPath builds the plugin-side path for an inbound request. The
// adapter key namespaces the plugin's routes so one plugin can serve many
// adapters without collisions.
func pluginPath(adapterKey, originalPath string) string {
	if originalPath == "" || originalPath[0] != '/' {
		originalPath = "/" + originalPath
	}
	return "/ether/v1/frontend/" + adapterKey + originalPath
}
