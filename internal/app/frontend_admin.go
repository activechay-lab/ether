package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/remote"
)

type frontendStreamCountsKey struct{}
type frontendStreamCounts struct {
	counts map[frontend.Protocol]int
	known  bool
}

// frontendControl persists desired state and reconciles it on each node.
type frontendControl struct {
	registry     *frontend.Registry
	listeners    *frontendListeners
	config       *config.Store
	mu           sync.Mutex
	applied      map[frontend.Protocol]listenerSettings
	clientCount  func(context.Context, frontend.Protocol) (int, bool)
	streamCounts func(context.Context) (map[frontend.Protocol]int, bool)
	sharedStatus func(frontend.Protocol, *frontend.Status)
	portChanged  func()
}
type listenerSettings struct{ Port, Discovery any }

func newFrontendControl(r *frontend.Registry, c *config.Store) *frontendControl {
	return &frontendControl{registry: r, listeners: newFrontendListeners(r), config: c, applied: make(map[frontend.Protocol]listenerSettings)}
}
func (f *frontendControl) descriptor(id frontend.Protocol) (frontend.Descriptor, bool) {
	for _, d := range f.registry.Descriptors() {
		if d.ID == id {
			return d, true
		}
	}
	return frontend.Descriptor{}, false
}
func (f *frontendControl) List(ctx context.Context) ([]admin.FrontendInfo, error) {
	cfg := f.config.Get()
	var counts map[frontend.Protocol]int
	var known bool
	if f.streamCounts != nil {
		counts, known = f.streamCounts(ctx)
		ctx = context.WithValue(ctx, frontendStreamCountsKey{}, frontendStreamCounts{counts: counts, known: known})
	}
	out := make([]admin.FrontendInfo, 0)
	for _, d := range f.registry.Descriptors() {
		enabled := cfg.Frontends[string(d.ID)].Enabled
		if cfg.Frontends == nil && frontendHostPolicy(d.ID).MainListener {
			enabled = true
		}
		status := f.listeners.status(ctx, d.ID)
		if f.sharedStatus != nil {
			f.sharedStatus(d.ID, &status)
		}
		if f.clientCount != nil {
			status.Clients24h, status.Clients24hKnown = f.clientCount(ctx, d.ID)
		}
		if known {
			status.LiveStreams, status.LiveStreamsKnown = counts[d.ID], true
		}
		out = append(out, admin.FrontendInfo{Descriptor: d, Enabled: enabled, Status: status})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return frontendRank(out[i].Descriptor.ID) < frontendRank(out[j].Descriptor.ID)
	})
	return out, nil
}

// Household order for the setup chooser and the Apps page; protocol names are UI identities.
func frontendRank(id frontend.Protocol) int {
	switch id {
	case "plex":
		return 0
	case "jellyfin":
		return 1
	case "emby":
		return 2
	case "stremio":
		return 3
	case "silo":
		return 4
	case "dlna":
		return 5
	case "webdav":
		return 6
	case "m3u":
		return 7
	default:
		return 8
	}
}
func (f *frontendControl) Settings(_ context.Context, id frontend.Protocol) (frontend.Settings, error) {
	if _, ok := f.descriptor(id); !ok {
		return nil, frontend.ErrNotRegistered
	}
	cfg := f.config.Read()
	if frontendHostPolicy(id).MainListener {
		return frontend.Settings{"port": float64(cfg.Server.Port)}, nil
	}
	settings := frontend.Settings{}
	for k, v := range cfg.Frontends[string(id)].Settings {
		settings[k] = v
	}
	if publicURL := strings.TrimSpace(cfg.Frontends[string(id)].PublicURL); publicURL != "" {
		settings["publicUrl"] = publicURL
	}
	if port := cfg.FrontendPorts[string(id)]; port > 0 {
		settings["port"] = float64(port)
	} else if port, ok := frontend.DefaultPort(id); ok {
		settings["port"] = float64(port)
	}
	return settings, nil
}
func validateFrontendSettings(s frontend.Settings, protocolValidator bool) error {
	for k, v := range s {
		switch k {
		case "port":
			n, ok := v.(float64)
			if !ok || n < 1 || n > 65535 || math.Trunc(n) != n {
				return errors.New("invalid port")
			}
		case "discovery", "quickConnect", "publicViewerPicker":
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("invalid %s", k)
			}
		default:
			if !protocolValidator {
				return fmt.Errorf("unknown setting %s", k)
			}
		}
	}
	return nil
}
func (f *frontendControl) PutSettings(_ context.Context, id frontend.Protocol, s frontend.Settings) error {
	if _, ok := f.descriptor(id); !ok {
		return frontend.ErrNotRegistered
	}
	s = withoutServerName(s)
	// publicUrl is a host-owned setting stored on config.Frontend, like the
	// port; keep it out of the protocol's own settings and validation. Plex
	// does not take one (it keeps Plex.CustomURLs), so leave it in place
	// there and let the Plex branch below reject it.
	publicURL, hasPublicURL := "", false
	if frontendHostPolicy(id).PublicURL {
		var err error
		s, publicURL, hasPublicURL, err = extractPublicURL(s)
		if err != nil {
			return err
		}
	}
	supported, err := f.registry.ValidateSettings(id, s)
	if err != nil {
		return err
	}
	if err := validateFrontendSettings(s, supported); err != nil {
		return err
	}
	if frontendHostPolicy(id).MainListener {
		for key := range s {
			if key != "port" {
				return fmt.Errorf("unsupported Plex setting %s", key)
			}
		}
		if os.Getenv("ETHER_PORT") != "" {
			return errors.New("ETHER_PORT controls the listening port")
		}
		oldPort := f.config.Get().Server.Port
		if port, ok := s["port"].(float64); ok && int(port) != oldPort {
			ln, err := net.Listen("tcp", net.JoinHostPort(f.config.Get().Server.BindAddress, strconv.Itoa(int(port))))
			if err != nil {
				return fmt.Errorf("listening port is unavailable: %w", err)
			}
			ln.Close()
		}
		err := f.config.Update(func(c *config.Config) error {
			if port, ok := s["port"].(float64); ok {
				c.Server.Port = int(port)
				c.Server.PortExplicit = true
			}
			return nil
		})
		if err == nil && oldPort != f.config.Get().Server.Port && f.portChanged != nil {
			f.portChanged()
		}
		return err
	}
	return f.config.Update(func(c *config.Config) error {
		if c.Frontends == nil {
			c.Frontends = map[string]config.Frontend{}
			c.Frontends["plex"] = config.Frontend{Enabled: true} // Preserve the default main-listener registration.
		}
		v := c.Frontends[string(id)]
		// Only a present publicUrl changes the stored address; a settings
		// save that never heard of it must not clear the owner's address.
		// A present empty value clears it.
		if hasPublicURL {
			v.PublicURL = publicURL
		}
		v.Settings = make(map[string]any, len(s))
		for k, val := range s {
			if k != "port" {
				v.Settings[k] = val
			}
		}
		if c.FrontendPorts == nil {
			c.FrontendPorts = make(map[string]int)
		}
		if port, ok := s["port"].(float64); ok {
			c.FrontendPorts[string(id)] = int(port)
		} else {
			delete(c.FrontendPorts, string(id))
		}
		c.Frontends[string(id)] = v
		return nil
	})
}
func (f *frontendControl) Toggle(_ context.Context, id frontend.Protocol, enabled bool) error {
	if _, ok := f.descriptor(id); !ok {
		return frontend.ErrNotRegistered
	}
	return f.config.Update(func(c *config.Config) error {
		if c.Frontends == nil {
			c.Frontends = map[string]config.Frontend{}
			if !frontendHostPolicy(id).MainListener {
				c.Frontends["plex"] = config.Frontend{Enabled: true} // Preserve the default main-listener registration.
			}
		}
		v := c.Frontends[string(id)]
		v.Enabled = enabled
		c.Frontends[string(id)] = v
		return nil
	})
}

// Reconcile is called on startup and after a shared configuration update.
func (f *frontendControl) Reconcile(ctx context.Context, c config.Config) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.registry.Descriptors() {
		desired := c.Frontends[string(d.ID)].Enabled
		if c.Frontends == nil && frontendHostPolicy(d.ID).MainListener {
			desired = true
		}
		settings := c.Frontends[string(d.ID)].Settings
		current := listenerSettings{Port: c.FrontendPorts[string(d.ID)], Discovery: settings["discovery"]}
		_, mounted := f.registry.Instance(d.ID)
		if desired && mounted && !reflect.DeepEqual(f.applied[d.ID], current) {
			_ = f.listeners.stop(ctx, d.ID)
			_, mounted = f.registry.Instance(d.ID)
		}
		if desired && !mounted {
			if f.listeners.start(ctx, d.ID) == nil {
				f.applied[d.ID] = current
			}
		} else if !desired && mounted {
			if f.listeners.stop(ctx, d.ID) == nil {
				delete(f.applied, d.ID)
			}
		}
	}
}

// extractPublicURL removes publicUrl from the settings and returns its
// normalized value (empty clears the stored address). Present reports
// whether the key was there at all.
func extractPublicURL(s frontend.Settings) (out frontend.Settings, value string, present bool, err error) {
	raw, present := s["publicUrl"]
	if !present {
		return s, "", false, nil
	}
	text, ok := raw.(string)
	if !ok {
		return s, "", true, errors.New("invalid public address")
	}
	text = strings.TrimSpace(text)
	if text != "" {
		c, parseErr := remote.ParseCustomURL(text)
		if parseErr != nil {
			return s, "", true, errors.New("invalid public address")
		}
		text = strings.TrimRight(c.URI, "/")
	}
	out = make(frontend.Settings, len(s))
	for k, v := range s {
		if k != "publicUrl" {
			out[k] = v
		}
	}
	return out, text, true, nil
}

func withoutServerName(s frontend.Settings) frontend.Settings {
	if _, ok := s["serverName"]; !ok {
		return s
	}
	out := make(frontend.Settings, len(s))
	for k, v := range s {
		if k != "serverName" {
			out[k] = v
		}
	}
	return out
}

var _ admin.FrontendAdmin = (*frontendControl)(nil)
