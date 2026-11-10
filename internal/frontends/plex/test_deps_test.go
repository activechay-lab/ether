package plex

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"testing"
	"time"

	_ "github.com/braedonsaunders/ether/internal/artwork/service"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/sessions"
)

type testHomeCache struct {
	Revision       func(context.Context, *frontend.User) (frontend.HomeRevision, error)
	NoProviderWait func(context.Context) context.Context
	Layout         func() uint64
	Config         func() uint64
	Catalog        func() uint64
}

type testServerDeps struct {
	Deps
	Experience          frontend.Experience
	HomeCache           *testHomeCache
	Catalog             frontend.Catalog
	Collections         frontend.Collections
	State               frontend.State
	Playlists           frontend.Playlists
	Recommender         frontend.Recommender
	VibeSearch          frontend.VibeSearcher
	Search              frontend.SearchSources
	FavoriteShows       func(context.Context, *frontend.User) (map[frontend.RatingKey]bool, error)
	FilterAvailable     func(context.Context, []frontend.Title) []frontend.Title
	JudgedOnFacts       func(context.Context) func(frontend.Title) bool
	PrioritizeAvailable func(context.Context, []frontend.Title) []frontend.Title
	EpisodeOrder        frontend.EpisodeOrder
	Discovery           *frontend.DiscoveryFeatures
	Dashboard           frontend.Dashboard
	Resources           frontend.ResourceStatistics
	LibraryTitles       func() (movies, shows string)
}

func hostOf[T any](d *T) *frontend.Host {
	if d == nil {
		return nil
	}
	switch v := any(d).(type) {
	case *Deps:
		return &v.Host
	case *testServerDeps:
		return &v.Deps.Host
	default:
		panic("unsupported deps pointer")
	}
}

func (d testServerDeps) experienceService() frontend.Experience {
	if d.Experience != nil {
		return d.Experience
	}
	var rev func(context.Context, *frontend.User) (frontend.HomeRevision, error)
	var noWait func(context.Context) context.Context
	if d.HomeCache != nil {
		rev = d.HomeCache.Revision
		noWait = d.HomeCache.NoProviderWait
	}
	return experience.NewDirectService(experience.ServiceDeps{
		Catalog:             d.Catalog,
		Collections:         d.Collections,
		State:               d.State,
		Playlists:           d.Playlists,
		Recommender:         d.Recommender,
		VibeSearch:          d.VibeSearch,
		SearchSources:       d.Search,
		FavoriteShows:       d.FavoriteShows,
		FilterAvailable:     d.FilterAvailable,
		JudgedOnFacts:       d.JudgedOnFacts,
		PrioritizeAvailable: d.PrioritizeAvailable,
		EpisodeOrder:        d.EpisodeOrder,
		Discovery:           d.Discovery,
		Dashboard:           d.Dashboard,
		Resources:           d.Resources,
		LibraryTitles:       d.LibraryTitles,
		HomeRevision:        rev,
		NoProviderWait:      noWait,
		Now:                 d.Deps.Host.Now,
	})
}

func (d testServerDeps) pageFreshnessFunc() func() frontend.PageFreshness {
	if d.HomeCache == nil {
		return nil
	}
	hc := d.HomeCache
	return func() frontend.PageFreshness {
		var f frontend.PageFreshness
		if hc.Layout != nil {
			f.Layout = hc.Layout()
		}
		if hc.Config != nil {
			f.Config = hc.Config()
		}
		if hc.Catalog != nil {
			f.Catalog = hc.Catalog()
		}
		return f
	}
}

// testStores returns the real sessions-backed store factory for tests.
func testStores() frontend.SessionStores { return sessions.NewSessionStores() }

// testDeps fills the required host factories in d from the real
// sessions implementations, leaving explicitly set fields alone.
func testDeps(d Deps) Deps {
	if d.SessionStores == nil {
		d.SessionStores = testStores()
	}
	if d.Host.Experience == nil {
		d.Host.Experience = experience.NewDirectService(experience.ServiceDeps{Now: d.Host.Now})
	}
	return d
}

func newTestServer(d testServerDeps) *Server {
	srv := NewServer(testDeps(d.Deps))
	srv.bindHost(frontend.Host{
		Experience:    d.experienceService(),
		PageFreshness: d.pageFreshnessFunc(),
	})
	return srv
}

func (s *Server) currentTestDeps() testServerDeps {
	if s == nil {
		return testServerDeps{}
	}
	deps := s.d
	deps.Host = s.host
	var libTitles func() (string, string)
	if m, sh := s.exp().LibraryTitles(); m != "" || sh != "" {
		libTitles = func() (string, string) { return m, sh }
	}
	return testServerDeps{
		Deps:                deps,
		Catalog:             s.catalog(),
		Collections:         s.collections(),
		State:               s.state(),
		Playlists:           s.playlists(),
		Recommender:         s.recommender(),
		VibeSearch:          s.vibeSearch(),
		Search:              s.searchSources(),
		FavoriteShows:       s.favoriteShows(),
		FilterAvailable:     s.filterAvailable(),
		JudgedOnFacts:       s.judgedOnFacts(),
		PrioritizeAvailable: s.prioritizeAvailable(),
		EpisodeOrder:        s.episodeOrder(),
		Discovery:           s.discovery(),
		Dashboard:           s.dashboard(),
		Resources:           s.resources(),
		LibraryTitles:       libTitles,
	}
}

func (s *Server) updateTestDeps(fn func(*testServerDeps)) {
	d := s.currentTestDeps()
	fn(&d)
	s.d = d.Deps
	s.bindHost(frontend.Host{
		Experience:    d.experienceService(),
		PageFreshness: d.pageFreshnessFunc(),
	})
}

func (s *Server) setCatalog(v frontend.Catalog) {
	s.updateTestDeps(func(d *testServerDeps) { d.Catalog = v })
}
func (s *Server) setCollections(v frontend.Collections) {
	s.updateTestDeps(func(d *testServerDeps) { d.Collections = v })
}
func (s *Server) setState(v frontend.State) {
	s.updateTestDeps(func(d *testServerDeps) { d.State = v })
}
func (s *Server) setPlaylists(v frontend.Playlists) {
	s.updateTestDeps(func(d *testServerDeps) { d.Playlists = v })
}
func (s *Server) setRecommender(v frontend.Recommender) {
	s.updateTestDeps(func(d *testServerDeps) { d.Recommender = v })
}
func (s *Server) setFilterAvailable(v func(context.Context, []frontend.Title) []frontend.Title) {
	s.updateTestDeps(func(d *testServerDeps) { d.FilterAvailable = v })
}
func (s *Server) setEpisodeOrder(v frontend.EpisodeOrder) {
	s.updateTestDeps(func(d *testServerDeps) { d.EpisodeOrder = v })
}
func (s *Server) setDiscovery(v *frontend.DiscoveryFeatures) {
	s.updateTestDeps(func(d *testServerDeps) { d.Discovery = v })
}
func (s *Server) setDashboard(v frontend.Dashboard) {
	s.updateTestDeps(func(d *testServerDeps) { d.Dashboard = v })
}
func (s *Server) setResources(v frontend.ResourceStatistics) {
	s.updateTestDeps(func(d *testServerDeps) { d.Resources = v })
}
func (s *Server) setLibraryTitles(v func() (string, string)) {
	s.updateTestDeps(func(d *testServerDeps) { d.LibraryTitles = v })
}

func (s *Server) setImageURL(fn frontend.ImageURLFunc) {
	if fn == nil {
		fn = func(path, size string) string { return "https://image.tmdb.org/t/p/" + size + path }
	}
	s.imageURL = fn
}

func (s *Server) setGeneratedPosters(g frontend.TitlePosterRenderer) {
	s.generatedPosters = g
}

func (s *Server) setLiveTV(ltv frontend.LiveTV) {
	s.liveTV = ltv
}

func (s *Server) setSupport(rec frontend.DiagnosticRecorder) {
	s.support = rec
}

func (s *Server) setClientSeen(fn func(string)) {
	s.clientSeen = fn
}

func (s *Server) setRemoteClientSeen(fn func(string)) {
	s.remoteClientSeen = fn
}

func testServer(d Deps) *Server {
	d = testDeps(d)
	return &Server{
		d:          d,
		host:       d.Host,
		experience: d.Host.Experience,
		log:        slog.Default(),
	}
}

func setDepsConfig[T any](d *T, fn func() config.Config) {
	h := hostOf(d)
	if h == nil {
		return
	}
	h.HostConfig = fn
	if fn == nil {
		h.Config = nil
		return
	}
	h.Config = func() frontend.Settings {
		return frontend.Settings{"serverName": fn().Server.FriendlyName}
	}
}

func depsConfig[T any](d *T) func() config.Config {
	h := hostOf(d)
	if h == nil {
		return nil
	}
	return h.HostConfig
}

func (s *Server) setTestConfig(fn func() config.Config) {
	s.host.HostConfig = fn
	if fn == nil {
		s.host.Config = nil
		return
	}
	s.host.Config = func() frontend.Settings {
		return frontend.Settings{"serverName": fn().Server.FriendlyName}
	}
}

func (s *Server) testConfig() func() config.Config {
	if s == nil {
		return nil
	}
	return s.host.HostConfig
}

func setDepsNow[T any](d *T, fn func() time.Time) {
	if h := hostOf(d); h != nil {
		h.Now = fn
	}
}

func depsNow[T any](d *T) func() time.Time {
	if h := hostOf(d); h != nil {
		return h.Now
	}
	return nil
}

func (s *Server) setTestNow(fn func() time.Time) {
	s.host.Now = fn
}

func setDepsIdentity[T any](d *T, id Identity) {
	h := hostOf(d)
	if h == nil {
		return
	}
	if id.MachineIdentifier != "" {
		h.ServerIdentity.MachineID = id.MachineIdentifier
	}
	if id.FriendlyName != "" {
		h.ServerIdentity.FriendlyName = id.FriendlyName
	}
	if id.Version != "" {
		h.ServerIdentity.Version = id.Version
	}
	if id.Platform != "" {
		h.ServerIdentity.Platform = id.Platform
	}
	if id.PlatformVersion != "" {
		h.ServerIdentity.PlatformVersion = id.PlatformVersion
	}
	if id.MyPlexSubscription || len(id.OwnerFeatures) > 0 {
		sub, feats := id.MyPlexSubscription, append([]string(nil), id.OwnerFeatures...)
		h.OwnerSubscription = func() (bool, []string) { return sub, feats }
	}
}

func (s *Server) setTestIdentity(id Identity) {
	s.host.ServerIdentity = frontend.ServerIdentity{
		MachineID:       id.MachineIdentifier,
		FriendlyName:    id.FriendlyName,
		Version:         id.Version,
		Platform:        id.Platform,
		PlatformVersion: id.PlatformVersion,
	}
	sub, feats := id.MyPlexSubscription, append([]string(nil), id.OwnerFeatures...)
	s.host.OwnerSubscription = func() (bool, []string) { return sub, feats }
}

func (s *Server) setTestSubscription(sub bool) {
	var feats []string
	if s.host.OwnerSubscription != nil {
		_, feats = s.host.OwnerSubscription()
	}
	s.host.OwnerSubscription = func() (bool, []string) { return sub, feats }
}

func (s *Server) setTestPlatformVersion(v string) {
	s.host.ServerIdentity.PlatformVersion = v
}

func setDepsOwnerSubscription[T any](d *T, fn func() (bool, []string)) {
	if h := hostOf(d); h != nil {
		h.OwnerSubscription = fn
	}
}

func (s *Server) setTestOwnerSubscription(fn func() (bool, []string)) {
	s.host.OwnerSubscription = fn
}

func setDepsRepoFileURL[T any](d *T, fn func(string) string) {
	if h := hostOf(d); h != nil {
		h.RepoFileURL = fn
	}
}

func (s *Server) setTestRepoFileURL(fn func(string) string) {
	s.host.RepoFileURL = fn
}

func setDepsPublicURL[T any](d *T, fn func() string) {
	if h := hostOf(d); h != nil {
		h.PublicOrigin = fn
	}
}

func (s *Server) setTestPublicURL(fn func() string) {
	s.host.PublicOrigin = fn
}

func setDepsAuth[T any](d *T, a core.Auth) {
	if h := hostOf(d); h != nil {
		h.Identity = frontend.BindAuth(a)
	}
}

func (s *Server) setTestAuth(a core.Auth) {
	s.host.Identity = frontend.BindAuth(a)
}

func (s *Server) testAuth() *fakeAuth {
	if u, ok := s.host.Identity.(interface{ UnwrapAuth() core.Auth }); ok {
		fa, _ := u.UnwrapAuth().(*fakeAuth)
		return fa
	}
	return nil
}

func setDepsPolicy[T any](d *T, p frontend.ViewerPolicy) {
	if h := hostOf(d); h != nil {
		h.ViewerPolicy = p
	}
}

func (s *Server) setTestPolicy(p frontend.ViewerPolicy) {
	s.host.ViewerPolicy = p
}

func setDepsHTTPClient[T any](d *T, c *http.Client) {
	if h := hostOf(d); h != nil {
		h.MetadataHTTP = c
	}
}

func setDepsDirectHTTP[T any](d *T, c *http.Client) {
	h := hostOf(d)
	if h == nil {
		return
	}
	if c == nil {
		h.DirectProxy = nil
		return
	}
	if tr, ok := c.Transport.(*http.Transport); ok && tr != nil && tr.Proxy != nil {
		h.DirectProxy = tr.Proxy
		return
	}
	h.DirectProxy = func(*http.Request) (*url.URL, error) { return nil, nil }
}

func TestNewServerRequiresSessionStores(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewServer without SessionStores did not panic")
		}
	}()
	NewServer(Deps{})
}
