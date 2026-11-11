package plex

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/braedonsaunders/ether/internal/frontend"
)

type frontendState struct {
	mu     sync.Mutex
	server *Server
	closed bool
}

// NewFrontend returns a Plex frontend that lazily builds its server from
// frontend.Host on first mount and retains it across toggles until Shutdown.
func NewFrontend() Frontend {
	return Frontend{state: &frontendState{}}
}

// Frontend adapts the PMS handler to the frontend registry.
// Unmount stops this mount's broker sessions, encoders, trickplay, and
// downloads, and drops its watch and home subscriptions. When constructed via
// NewFrontend, the process-owned server stays up across toggles so an
// in-flight request can finish and is closed on Shutdown.
type Frontend struct {
	NewServer    func() (*Server, error)
	Server       *Server
	WatchRefresh frontend.WatchRefreshSubscriber
	// ExplicitWatchOnly preserves spike mode: only cross-surface scrobbles refresh.
	ExplicitWatchOnly bool
	StatusFunc        func(context.Context) frontend.Status
	ListenersFunc     func() []frontend.ListenerSpec

	state *frontendState
}

func (f Frontend) Describe() frontend.Descriptor {
	return frontend.Descriptor{ID: "plex", Name: "Plex", DefaultPort: 32400, SettingsSchema: frontend.PortSchema()}
}

// HasRetainedServer reports whether this frontend has constructed an active server.
func (f Frontend) HasRetainedServer() bool {
	if f.Server != nil {
		return !f.Server.Closed()
	}
	if f.state == nil {
		return false
	}
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	return f.state.server != nil && !f.state.server.Closed()
}

// Shutdown closes any retained server and prevents future mounts on this frontend.
func (f Frontend) Shutdown(context.Context) error {
	if f.state == nil {
		return nil
	}
	f.state.mu.Lock()
	f.state.closed = true
	srv := f.state.server
	f.state.server = nil
	f.state.mu.Unlock()
	if srv != nil {
		return srv.Close()
	}
	return nil
}

// Prune prunes derived rows owned by the retained server, if constructed.
func (f Frontend) Prune(ctx context.Context) (int64, error) {
	if f.state != nil {
		f.state.mu.Lock()
		srv := f.state.server
		f.state.mu.Unlock()
		if srv != nil && !srv.Closed() {
			return srv.Prune(ctx)
		}
		return 0, nil
	}
	if f.Server != nil && !f.Server.Closed() {
		return f.Server.Prune(ctx)
	}
	return 0, nil
}

func (f Frontend) Mount(ctx context.Context, host frontend.Host) (frontend.Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	srv := f.Server
	ownsServer := false
	boundAtCreate := false
	if srv == nil && f.state != nil {
		f.state.mu.Lock()
		if err := ctx.Err(); err != nil {
			f.state.mu.Unlock()
			return nil, err
		}
		if f.state.closed {
			f.state.mu.Unlock()
			return nil, errors.New("plex: frontend is shut down")
		}
		if f.state.server == nil || f.state.server.Closed() {
			if f.NewServer != nil {
				built, err := f.NewServer()
				if err != nil {
					f.state.mu.Unlock()
					return nil, err
				}
				f.state.server = built
			} else {
				if _, ok := host.Sessions.(frontend.SessionRuntime); !ok {
					f.state.mu.Unlock()
					return nil, errors.New("plex: session runtime is unavailable")
				}
				f.state.server = NewServer(Deps{
					Host:          host,
					TracePlayback: host.TracePlayback,
				})
				boundAtCreate = true
			}
		}
		srv = f.state.server
		f.state.mu.Unlock()
	} else if srv == nil && f.NewServer != nil {
		var err error
		srv, err = f.NewServer()
		if err != nil {
			return nil, err
		}
	} else if srv == nil {
		if _, ok := host.Sessions.(frontend.SessionRuntime); ok {
			srv = NewServer(Deps{
				Host:          host,
				TracePlayback: host.TracePlayback,
			})
			ownsServer = true
			boundAtCreate = true
		}
	}
	if srv == nil {
		return nil, errors.New("plex: frontend server is unavailable")
	}
	if err := ctx.Err(); err != nil {
		if ownsServer {
			_ = srv.Close()
		}
		return nil, err
	}
	if !boundAtCreate {
		srv.bindHost(host)
	}

	explicitOnly := f.ExplicitWatchOnly || (host.HostConfig != nil && host.HostConfig().Spike.Enabled)
	instance := &plexInstance{
		sessions:   host.Sessions,
		server:     srv,
		ownsServer: ownsServer,
		status:     f.StatusFunc,
		listeners:  f.ListenersFunc,
	}
	if f.WatchRefresh != nil {
		instance.unsubscribe = f.WatchRefresh(srv.WatchStateChanged, explicitOnly)
	} else if host.WatchRefresh != nil {
		instance.unsubscribe = host.WatchRefresh(srv.WatchStateChanged, explicitOnly)
	}
	if !explicitOnly && host.HomeInvalidation != nil {
		instance.unsubHome = host.HomeInvalidation(srv.ViewerHomeChanged)
	}
	return instance, nil
}

type plexInstance struct {
	server *Server
	// sessions is this mount's broker. The retained server is updated on each
	// mount so its playback handlers publish through the current broker.
	sessions    frontend.Sessions
	ownsServer  bool
	unsubscribe func()
	unsubHome   func()
	closeOnce   sync.Once
	status      func(context.Context) frontend.Status
	listeners   func() []frontend.ListenerSpec
}

func (i *plexInstance) Handler() http.Handler { return i.server }

// Plex shares the existing owner-console listener; the app owns that socket.
func (i *plexInstance) Listeners() []frontend.ListenerSpec {
	if i.listeners != nil {
		return i.listeners()
	}
	return []frontend.ListenerSpec{{Shared: true, PathPrefix: "/"}}
}
func (i *plexInstance) Discovery() []frontend.Announcer { return nil }
func (i *plexInstance) Status(ctx context.Context) frontend.Status {
	if i.status != nil {
		return i.status(ctx)
	}
	version := DefaultPMSVersion
	if i.server != nil {
		version = i.server.identity().Version
	}
	return frontend.Status{
		TargetVersion:    version,
		LAN:              "unknown",
		WAN:              "unknown",
		Conformance:      "unknown",
		LiveStreamsKnown: true,
	}
}
func (i *plexInstance) Close(ctx context.Context) error {
	i.closeOnce.Do(func() {
		if i.unsubscribe != nil {
			i.unsubscribe()
		}
		if i.unsubHome != nil {
			i.unsubHome()
		}
		if stop, ok := i.sessions.(frontend.ProtocolStopper); ok {
			_ = stop.StopProtocol(ctx, "plex")
		}
		if i.server != nil {
			i.server.unmountRuntime(i.sessions)
			if i.ownsServer {
				_ = i.server.Close()
			}
		}
	})
	return nil
}

func (i *plexInstance) NoteOwnerStop(sessionID, reason string) {
	if i != nil && i.server != nil {
		i.server.NoteOwnerStop(sessionID, reason)
	}
}

func (i *plexInstance) NotifyLiveLineupChanged(viewerID int64) {
	if i == nil || i.server == nil {
		return
	}
	if viewerID > 0 {
		i.server.NotifyLiveLineupFor(viewerID)
		return
	}
	i.server.NotifyLiveLineup()
}

func (i *plexInstance) SupportViewer(ctx context.Context, viewer *frontend.User) (frontend.DiagnosticViewerFacts, error) {
	if i == nil || i.server == nil {
		return frontend.DiagnosticViewerFacts{}, errors.New("Plex unavailable")
	}
	return i.server.SupportViewer(ctx, viewer)
}

func (i *plexInstance) BackgroundBusy() int {
	if i == nil || i.server == nil {
		return 0
	}
	return i.server.BackgroundBusy()
}

func (i *plexInstance) Prune(ctx context.Context) (int64, error) {
	if i == nil || i.server == nil || i.server.Closed() {
		return 0, nil
	}
	return i.server.Prune(ctx)
}

func (i *plexInstance) PublishedLibraries() []frontend.PublishedLibrarySection {
	if i == nil || i.server == nil {
		return nil
	}
	secs := i.server.LibrarySections()
	out := make([]frontend.PublishedLibrarySection, len(secs))
	for idx, sec := range secs {
		out[idx] = frontend.PublishedLibrarySection{
			ID:       sec.ID,
			Type:     sec.Type,
			Title:    sec.Title,
			UUID:     sec.UUID,
			Language: sec.Language,
		}
	}
	return out
}

var (
	_ frontend.Frontend                 = Frontend{}
	_ frontend.Shutdowner               = Frontend{}
	_ frontend.DerivedPruner            = Frontend{}
	_ frontend.Instance                 = (*plexInstance)(nil)
	_ frontend.OwnerSessionStopper      = (*plexInstance)(nil)
	_ frontend.LiveLineupObserver       = (*plexInstance)(nil)
	_ frontend.ViewerDiagnostics        = (*plexInstance)(nil)
	_ frontend.BackgroundWorker         = (*plexInstance)(nil)
	_ frontend.DerivedPruner            = (*plexInstance)(nil)
	_ frontend.PublishedLibraryProvider = (*plexInstance)(nil)
)
