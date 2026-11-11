package frontendadapter

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// Registry is the slice of frontend.Registry the controller drives. It is
// an interface so the lifecycle is testable without a real registry.
type Registry interface {
	Register(frontend.Frontend) error
	Unregister(frontend.Protocol) error
	Mount(ctx context.Context, id frontend.Protocol) (frontend.Instance, error)
	Unmount(ctx context.Context, id frontend.Protocol) error
	Instance(id frontend.Protocol) (frontend.Instance, bool)
}

// Catalog reports the adapter declarations of currently installed, enabled
// plugins. App wiring implements it from the plugin store; the controller
// never touches the store itself.
type Catalog interface {
	// EnabledAdapters returns one Declaration per adapter of every enabled
	// plugin. A disabled, removed or uninstalled plugin contributes none.
	EnabledAdapters(ctx context.Context) ([]Declaration, error)
}

// Controller keeps the registry's adapter frontends in step with the
// installed plugin set: adapters register on enable and unregister on
// disable/remove, without restarting Ether (spec §6.5).
type Controller struct {
	reg     Registry
	mgr     Manager
	catalog Catalog

	mu   sync.Mutex
	live map[frontend.Protocol]Declaration
}

// NewController builds the lifecycle controller.
func NewController(reg Registry, mgr Manager, catalog Catalog) (*Controller, error) {
	if reg == nil || mgr == nil || catalog == nil {
		return nil, errors.New("frontendadapter: controller needs a registry, manager and catalog")
	}
	return &Controller{reg: reg, mgr: mgr, catalog: catalog, live: make(map[frontend.Protocol]Declaration)}, nil
}

// Sync reconciles registered adapters with the catalog: new adapters are
// registered, adapters whose plugin is gone are unregistered. It is called
// after every install, enable, disable, upgrade and remove.
func (c *Controller) Sync(ctx context.Context) error {
	want, err := c.catalog.EnabledAdapters(ctx)
	if err != nil {
		return err
	}
	desired := make(map[frontend.Protocol]Declaration, len(want))
	for _, d := range want {
		desired[AdapterID(d.PluginID, d.Meta.Key)] = d
	}
	var errs []error
	// Register adapters that appeared, and re-register ones whose
	// declaration changed (a plugin upgrade may alter libraries, prefix or
	// auth). Re-registering unmounts first, so the new definition takes
	// effect without a restart.
	ids := make([]frontend.Protocol, 0, len(desired))
	for id := range desired {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		c.mu.Lock()
		prev, known := c.live[id]
		c.mu.Unlock()
		if known && reflect.DeepEqual(prev, desired[id]) {
			continue
		}
		if known {
			if err := c.unregister(ctx, id); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		if err := c.register(ctx, desired[id]); err != nil {
			errs = append(errs, err)
		}
	}
	// Unregister adapters whose plugin is gone.
	c.mu.Lock()
	stale := make([]frontend.Protocol, 0)
	for id := range c.live {
		if _, ok := desired[id]; !ok {
			stale = append(stale, id)
		}
	}
	c.mu.Unlock()
	sort.Slice(stale, func(i, j int) bool { return stale[i] < stale[j] })
	for _, id := range stale {
		if err := c.unregister(ctx, id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Enable registers (and mounts, if the owner enabled it) the adapters of
// one plugin. It is idempotent.
func (c *Controller) Enable(ctx context.Context, decls []Declaration, mount bool) error {
	var errs []error
	for _, d := range decls {
		id := AdapterID(d.PluginID, d.Meta.Key)
		c.mu.Lock()
		_, known := c.live[id]
		c.mu.Unlock()
		if !known {
			if err := c.register(ctx, d); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		if mount {
			if _, err := c.reg.Mount(ctx, id); err != nil && !errors.Is(err, frontend.ErrAlreadyMounted) {
				errs = append(errs, fmt.Errorf("mount %s: %w", id, err))
			}
		}
	}
	return errors.Join(errs...)
}

// Disable unmounts and unregisters every adapter of one plugin.
func (c *Controller) Disable(ctx context.Context, pluginID string) error {
	c.mu.Lock()
	ids := make([]frontend.Protocol, 0)
	for id, d := range c.live {
		if d.PluginID == pluginID {
			ids = append(ids, id)
		}
	}
	c.mu.Unlock()
	var errs []error
	for _, id := range ids {
		if err := c.unregister(ctx, id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// register adds one adapter frontend to the registry.
func (c *Controller) register(_ context.Context, decl Declaration) error {
	f, err := New(decl, c.mgr)
	if err != nil {
		return err
	}
	if err := c.reg.Register(f); err != nil {
		return fmt.Errorf("register %s: %w", f.ID(), err)
	}
	c.mu.Lock()
	c.live[f.ID()] = decl
	c.mu.Unlock()
	return nil
}

// unregister unmounts (if mounted) then removes one adapter frontend.
func (c *Controller) unregister(ctx context.Context, id frontend.Protocol) error {
	if _, mounted := c.reg.Instance(id); mounted {
		if err := c.reg.Unmount(ctx, id); err != nil {
			return fmt.Errorf("unmount %s: %w", id, err)
		}
	}
	if err := c.reg.Unregister(id); err != nil && !errors.Is(err, frontend.ErrNotRegistered) {
		return fmt.Errorf("unregister %s: %w", id, err)
	}
	c.mu.Lock()
	delete(c.live, id)
	c.mu.Unlock()
	return nil
}

// Live reports the currently registered adapter protocols, sorted.
func (c *Controller) Live() []frontend.Protocol {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]frontend.Protocol, 0, len(c.live))
	for id := range c.live {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
