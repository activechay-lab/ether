package app

import (
	"context"
	"net/http"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/frontendadapter"
)

// adapterCatalog implements frontendadapter.Catalog from the plugin store:
// it reports one declaration per frontendAdapters[] entry of every enabled
// plugin, resolving each declared media-kind key to the library that kind
// created. A kind whose library does not exist yet is skipped, so an
// adapter never advertises a library the viewer could not open.
type adapterCatalog struct {
	app *App
}

func (c adapterCatalog) EnabledAdapters(ctx context.Context) ([]frontendadapter.Declaration, error) {
	if c.app.pluginsRT == nil || c.app.pluginsRT.mgr == nil {
		return nil, nil
	}
	recs, err := c.app.pluginsRT.mgr.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []frontendadapter.Declaration
	for _, rec := range recs {
		if !rec.Enabled || rec.Manifest.Ether == nil {
			continue
		}
		man := rec.Manifest
		man.Ether = man.EffectiveEther()
		if man.Ether == nil || !man.HasCapability(plugins.CapFrontendAdapter) {
			continue
		}
		for _, meta := range man.Ether.Contributes.FrontendAdapters {
			decl := frontendadapter.Declaration{
				PluginID:       rec.ID,
				Meta:           meta,
				Name:           strings.TrimSpace(meta.Label),
				Icon:           rec.Manifest.Logo,
				Version:        rec.Version,
				DocsURL:        rec.Manifest.Ether.Repo,
				SettingsSchema: rec.Manifest.Ether.Settings,
			}
			decl.Libraries = c.resolveLibraries(ctx, rec.ID, meta.Libraries)
			out = append(out, decl)
		}
	}
	return out, nil
}

// resolveLibraries maps an adapter's declared media-kind keys to the
// library ids those kinds created. A key with no library yet contributes
// nothing; the adapter then sees an empty allowed set for it.
func (c adapterCatalog) resolveLibraries(ctx context.Context, pluginID string, kindKeys []string) []core.LibraryID {
	if len(kindKeys) == 0 || c.app.libraries == nil {
		return nil
	}
	out := make([]core.LibraryID, 0, len(kindKeys))
	for _, key := range kindKeys {
		lib, err := c.app.libraries.ByKey(ctx, core.PluginLibraryKey(pluginID, key))
		if err != nil {
			continue
		}
		out = append(out, lib.ID)
	}
	return out
}

// buildAdapterController constructs the FrontendAdapter lifecycle controller
// and keeps it in step with plugin install/enable/disable/upgrade/remove.
func (a *App) buildAdapterController(registry *frontend.Registry) (*frontendadapter.Controller, error) {
	if a.pluginsRT == nil || a.pluginsRT.mgr == nil {
		return nil, nil
	}
	ctrl, err := frontendadapter.NewController(registry, a.pluginsRT.mgr, adapterCatalog{app: a})
	if err != nil {
		return nil, err
	}
	a.pluginsRT.adapterCtrl = ctrl
	// OnSync fires for every already-installed plugin immediately, so the
	// initial registration is the same call path as later changes.
	a.pluginsRT.mgr.OnSync(func(ctx context.Context, _ plugins.Record) { a.syncAdapters(ctx) })
	a.pluginsRT.mgr.OnRemove(func(ctx context.Context, _ string) { a.syncAdapters(ctx) })
	return ctrl, nil
}

// syncAdapters reconciles registered adapter frontends with the installed
// plugin set, then asks the frontend runtime to mount any adapter the owner
// already enabled. Errors are logged, not fatal: one bad adapter must not
// stop the others.
func (a *App) syncAdapters(ctx context.Context) {
	if a.pluginsRT == nil || a.pluginsRT.adapterCtrl == nil {
		return
	}
	if err := a.pluginsRT.adapterCtrl.Sync(ctx); err != nil {
		a.log.Warn("frontend adapter sync", "err", err)
	}
	// a.frontends is nil while the runtime is still being constructed; the
	// runtime's own start/reconcile mounts enabled adapters then.
	if a.frontends != nil {
		a.frontends.reconcile(a.cfg.Get())
	}
}

// adapterStreamURL builds the host backend behind Host.StreamURL (spec
// §6.5): it mints a scoped stream ticket for one of a plugin's own kind
// items for one viewer, after re-checking item ownership and library
// visibility. The item must belong to the calling plugin and its library
// must be visible to the viewer; anything else is refused.
func (a *App) adapterStreamURL(issuer *frontendadapter.TicketIssuer) func(ctx context.Context, pluginID string, viewerID int64, itemRef string) (string, error) {
	return func(ctx context.Context, pluginID string, viewerID int64, itemRef string) (string, error) {
		if issuer == nil || a.pluginsRT == nil || a.pluginsRT.pluginitems == nil || a.libraries == nil {
			return "", plugins.ErrUnavailable
		}
		ref, err := core.ParseItemRef(itemRef)
		if err != nil {
			return "", core.ErrNotFound
		}
		if ref.Plugin() != pluginID {
			return "", plugins.ErrPermissionDenied
		}
		if viewerID <= 0 || a.users == nil {
			return "", plugins.ErrPermissionDenied
		}
		viewer, err := a.users.PolicyUser(ctx, viewerID)
		if err != nil || viewer == nil {
			return "", plugins.ErrPermissionDenied
		}
		_, libID, err := a.pluginsRT.pluginitems.GetByRef(ctx, ref)
		if err != nil {
			return "", core.ErrNotFound
		}
		lib, err := a.libraries.Get(ctx, libID)
		if err != nil || lib.Hidden || !viewer.AllowsLibrary(lib.ID) {
			return "", core.ErrNotFound
		}
		return issuer.StreamURL(frontendadapter.StreamGrant{
			ViewerID: viewerID,
			ItemID:   ref.String(),
			Adapter:  pluginID,
		})
	}
}

// mountAdapterMedia registers the public kind-media endpoint. It is not
// LAN-only: a remote client follows the short-lived ticket URL from a
// plugin adapter response, and the ticket itself is the credential.
func (a *App) mountAdapterMedia(mux *http.ServeMux) {
	h := a.adapterMediaHandler()
	if h == nil {
		return
	}
	mux.Handle(frontendadapter.KindMediaPath, h)
}

// adapterMediaHandler builds the /ether/v1/kind-media/{ticket} endpoint. It
// returns nil when the kind resolver is unavailable, so the route stays
// unmounted.
func (a *App) adapterMediaHandler() *frontendadapter.MediaHandler {
	if a.pluginsRT == nil || a.pluginsRT.kindResolver == nil || a.pluginsRT.pluginitems == nil {
		return nil
	}
	issuer := a.pluginsRT.adapterTickets
	if issuer == nil {
		return nil
	}
	items := a.pluginsRT.pluginitems
	resolver := a.pluginsRT.kindResolver
	return frontendadapter.NewMediaHandler(frontendadapter.MediaDeps{
		Tickets: issuer,
		Viewer: func(ctx context.Context, id int64) (*core.Viewer, error) {
			if a.users == nil {
				return nil, core.ErrNotFound
			}
			return a.users.PolicyUser(ctx, id)
		},
		Open: func(ctx context.Context, viewer *core.Viewer, itemRef string, accept []string) (*frontend.KindMedia, error) {
			ref, err := core.ParseItemRef(itemRef)
			if err != nil {
				return nil, err
			}
			item, _, err := items.GetByRef(ctx, ref)
			if err != nil {
				return nil, err
			}
			return resolver.OpenKind(ctx, viewer, item.Key, accept)
		},
	})
}
