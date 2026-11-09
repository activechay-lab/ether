// Package silo translates Silo's native API into the frontend host contracts.
package silo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/conformance"
	"github.com/braedonsaunders/ether/internal/frontend/kit/route"
	"github.com/braedonsaunders/ether/internal/frontend/kit/support"
)

// Frontend is the independent Silo surface.
type Frontend struct{ MachineID string }

func (Frontend) Describe() frontend.Descriptor {
	return frontend.Descriptor{ID: "silo", Name: "Silo", DefaultPort: 8090, SettingsSchema: frontend.NamedServerSchema()}
}
func (f Frontend) Mount(_ context.Context, host frontend.Host) (frontend.Instance, error) {
	if f.MachineID == "" || host.Identity == nil {
		return nil, errors.New("silo requires server identity and authentication")
	}
	sum := sha256.Sum256([]byte("ether:silo:" + f.MachineID))
	i := &instance{host: host, id: hex.EncodeToString(sum[:16]), router: &route.Router{}}
	if err := i.publicRoutes(); err != nil {
		return nil, err
	}
	if err := i.imageRoutes(); err != nil {
		return nil, err
	}
	if err := i.eventRoutes(); err != nil {
		return nil, err
	}
	if err := i.authRoutes(); err != nil {
		return nil, err
	}
	if err := i.playbackRoutes(); err != nil {
		return nil, err
	}
	if err := i.watchPartyRoutes(); err != nil {
		return nil, err
	}
	if err := i.subtitleRoutes(); err != nil {
		return nil, err
	}
	if err := i.catalogRoutes(); err != nil {
		return nil, err
	}
	if err := i.playlistRoutes(); err != nil {
		return nil, err
	}
	if err := i.mutationRoutes(); err != nil {
		return nil, err
	}
	if err := i.notInterestedRoutes(); err != nil {
		return nil, err
	}
	if err := i.settingsRoutes(); err != nil {
		return nil, err
	}
	if err := i.browseRoutes(); err != nil {
		return nil, err
	}
	if err := i.peopleRoutes(); err != nil {
		return nil, err
	}
	return i, nil
}

type instance struct {
	host   frontend.Host
	id     string
	router *route.Router

	sectionsMu    sync.Mutex
	sectionsCache map[string]siloSectionPage
	// sectionsGen counts each viewer's own changes (dropViewerSections).
	sectionsGen map[int64]uint64

	plansMu sync.Mutex
	plans   map[string]playbackPlan

	liveMu    sync.Mutex
	liveCards map[string]liveChannelCardMeta
}

func (i *instance) Handler() http.Handler {
	next := support.Wrap(i.host.Support, "silo", func(r *http.Request) (*frontend.Viewer, frontend.Device) {
		v, _, err := i.deviceViewer(r)
		if err != nil {
			return nil, frontend.Device{}
		}
		return v, device(r)
	}, func(path string) string { return path }, i.router)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/silo/") {
			r2 := r.Clone(context.WithValue(r.Context(), sharedPrefix{}, true))
			u := *r.URL
			u.Path = strings.TrimPrefix(u.Path, "/silo")
			r2.URL = &u
			next.ServeHTTP(w, r2)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (i *instance) Listeners() []frontend.ListenerSpec {
	port := 8090
	if i.host.Config != nil {
		if n, ok := i.host.Config()["port"].(float64); ok && n >= 1 && n <= 65535 && n == float64(int(n)) {
			port = int(n)
		}
	}
	// On the shared main port Silo answers under /silo and at the root
	// /api/v2 (plus /api/v1 for the health probe), so a client given the
	// bare main address reaches it without a path. No other surface on
	// that port uses /api/v1 or /api/v2: Plex serves its own routes, the
	// console lives under /ether, and the other apps sit under their own
	// prefixes. Root requests carry no sharedPrefix marker, so the URLs
	// Silo returns stay root-relative and correct.
	return []frontend.ListenerSpec{{Address: fmt.Sprintf(":%d", port)}, {Shared: true, PathPrefix: "/silo"}, {Shared: true, PathPrefix: "/api/v2"}, {Shared: true, PathPrefix: "/api/v1"}}
}
func (*instance) Discovery() []frontend.Announcer { return nil }
func (*instance) Status(context.Context) frontend.Status {
	return frontend.Status{TargetVersion: "2", Conformance: conformance.Status("silo", "2")}
}
func (*instance) Close(context.Context) error { return nil }
func (i *instance) Routes() []route.Route     { return i.router.Routes() }
