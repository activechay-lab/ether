package plex

import (
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

// testStores returns the real sessions-backed store factory for tests.
func testStores() frontend.SessionStores { return sessions.NewSessionStores() }

// testDeps fills the required host factories in d from the real
// sessions/experience implementations, leaving explicitly set fields alone.
func testDeps(d Deps) Deps {
	if d.SessionStores == nil {
		d.SessionStores = testStores()
	}
	if d.Experience == nil {
		d.Experience = experience.NewSelectors()
	}
	return d
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
		d:    d,
		host: d.Host,
		log:  slog.Default(),
	}
}

func setDepsConfig(d *Deps, fn func() config.Config) {
	d.Host.HostConfig = fn
	if fn == nil {
		d.Host.Config = nil
		return
	}
	d.Host.Config = func() frontend.Settings {
		return frontend.Settings{"serverName": fn().Server.FriendlyName}
	}
}

func depsConfig(d *Deps) func() config.Config {
	if d == nil {
		return nil
	}
	return d.Host.HostConfig
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

func setDepsNow(d *Deps, fn func() time.Time) {
	d.Host.Now = fn
}

func depsNow(d *Deps) func() time.Time {
	if d == nil {
		return nil
	}
	return d.Host.Now
}

func (s *Server) setTestNow(fn func() time.Time) {
	s.host.Now = fn
}

func setDepsIdentity(d *Deps, id Identity) {
	if id.MachineIdentifier != "" {
		d.Host.ServerIdentity.MachineID = id.MachineIdentifier
	}
	if id.FriendlyName != "" {
		d.Host.ServerIdentity.FriendlyName = id.FriendlyName
	}
	if id.Version != "" {
		d.Host.ServerIdentity.Version = id.Version
	}
	if id.Platform != "" {
		d.Host.ServerIdentity.Platform = id.Platform
	}
	if id.PlatformVersion != "" {
		d.Host.ServerIdentity.PlatformVersion = id.PlatformVersion
	}
	if id.MyPlexSubscription || len(id.OwnerFeatures) > 0 {
		sub, feats := id.MyPlexSubscription, append([]string(nil), id.OwnerFeatures...)
		d.Host.OwnerSubscription = func() (bool, []string) { return sub, feats }
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

func setDepsOwnerSubscription(d *Deps, fn func() (bool, []string)) {
	d.Host.OwnerSubscription = fn
}

func (s *Server) setTestOwnerSubscription(fn func() (bool, []string)) {
	s.host.OwnerSubscription = fn
}

func setDepsRepoFileURL(d *Deps, fn func(string) string) {
	d.Host.RepoFileURL = fn
}

func (s *Server) setTestRepoFileURL(fn func(string) string) {
	s.host.RepoFileURL = fn
}

func setDepsPublicURL(d *Deps, fn func() string) {
	d.Host.PublicOrigin = fn
}

func (s *Server) setTestPublicURL(fn func() string) {
	s.host.PublicOrigin = fn
}

func setDepsAuth(d *Deps, a core.Auth) {
	d.Host.Identity = frontend.BindAuth(a)
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

func setDepsPolicy(d *Deps, p frontend.ViewerPolicy) {
	d.Host.ViewerPolicy = p
}

func (s *Server) setTestPolicy(p frontend.ViewerPolicy) {
	s.host.ViewerPolicy = p
}

func setDepsHTTPClient(d *Deps, c *http.Client) {
	d.Host.MetadataHTTP = c
}

func setDepsDirectHTTP(d *Deps, c *http.Client) {
	if c == nil {
		d.Host.DirectProxy = nil
		return
	}
	if tr, ok := c.Transport.(*http.Transport); ok && tr != nil && tr.Proxy != nil {
		d.Host.DirectProxy = tr.Proxy
		return
	}
	d.Host.DirectProxy = func(*http.Request) (*url.URL, error) { return nil, nil }
}

func TestNewServerRequiresExperience(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewServer without Experience did not panic")
		}
	}()
	NewServer(Deps{SessionStores: testStores()})
}

func TestNewServerRequiresSessionStores(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewServer without SessionStores did not panic")
		}
	}()
	NewServer(Deps{Experience: experience.NewSelectors()})
}
