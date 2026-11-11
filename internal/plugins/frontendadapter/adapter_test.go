package frontendadapter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// fakeIdentity resolves one credential to one viewer.
type fakeIdentity struct {
	viewers map[string]*core.Viewer
}

func (f fakeIdentity) Resolve(_ context.Context, _ string, credential string) (*core.Viewer, error) {
	if v, ok := f.viewers[credential]; ok {
		return v, nil
	}
	return nil, errors.New("no such credential")
}

// fakeManager records calls and answers from a scripted handler.
type fakeManager struct {
	calls   []plugins.Request
	handler func(plugins.Request) (plugins.Response, io.ReadCloser, error)
	// hosted makes CallStream fail with ErrStreamUnsupported so the
	// buffered Call path is exercised.
	hosted bool
	buff   func(plugins.Request) (plugins.Response, error)
}

func (m *fakeManager) CallStream(_ context.Context, pluginID string, req plugins.Request) (plugins.Response, io.ReadCloser, error) {
	req.PluginID = pluginID
	m.calls = append(m.calls, req)
	if m.hosted {
		return plugins.Response{}, nil, plugins.ErrStreamUnsupported
	}
	return m.handler(req)
}

func (m *fakeManager) Call(_ context.Context, pluginID string, req plugins.Request) (plugins.Response, error) {
	req.PluginID = pluginID
	m.calls = append(m.calls, req)
	if m.buff == nil {
		return plugins.Response{}, errors.New("no buffered handler")
	}
	return m.buff(req)
}

func viewerWithLibraries(id int64, name string, libs ...core.LibraryID) *core.Viewer {
	return &core.Viewer{ID: id, Title: name, AllowedLibraries: libs}
}

func testDecl(libs ...core.LibraryID) Declaration {
	return Declaration{
		PluginID: "plug1",
		Meta: plugins.FrontendAdapterMeta{
			Key:          "feed",
			Label:        "Records feed",
			ProtocolHint: "opds",
			PathPrefix:   "/opds",
			Auth:         AuthEtherToken,
		},
		Name:      "Records feed",
		Libraries: libs,
	}
}

func testHost(viewers map[string]*core.Viewer) frontend.Host {
	return frontend.Host{Identity: frontend.BindIdentity(fakeIdentity{viewers: viewers}, "plugin:plug1:feed")}
}

// TestProxyInjectsViewerHeadersAndHidesCredential proves the plugin never
// sees an Ether credential and receives the viewer headers §6.5 defines.
func TestProxyInjectsViewerHeadersAndHidesCredential(t *testing.T) {
	viewer := viewerWithLibraries(7, "Alice", core.LibraryMovies)
	mgr := &fakeManager{handler: func(req plugins.Request) (plugins.Response, io.ReadCloser, error) {
		return plugins.Response{Status: 200, Headers: map[string]string{"Content-Type": "application/atom+xml"}}, io.NopCloser(strings.NewReader("ok")), nil
	}}
	decl := testDecl(core.LibraryMovies)
	f, err := New(decl, mgr)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := f.Mount(context.Background(), testHost(map[string]*core.Viewer{"tok1": viewer}))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/opds/catalog?q=hi", nil)
	req.Header.Set("Authorization", "Bearer tok1")
	req.Header.Set("X-Ether-Viewer", "999")
	req.Header.Set("Cookie", "session=secret")
	rec := httptest.NewRecorder()
	inst.Handler().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(mgr.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(mgr.calls))
	}
	got := mgr.calls[0]
	if got.Path != "/ether/v1/frontend/feed/catalog" {
		t.Errorf("path = %q", got.Path)
	}
	if got.Query["q"] != "hi" {
		t.Errorf("query = %v", got.Query)
	}
	if got.Headers[HeaderViewer] != "7" {
		t.Errorf("viewer header = %q, want 7", got.Headers[HeaderViewer])
	}
	if got.Headers[HeaderViewerName] != "Alice" {
		t.Errorf("viewer name = %q", got.Headers[HeaderViewerName])
	}
	if got.Headers[HeaderLibraries] != "1" {
		t.Errorf("libraries = %q, want 1", got.Headers[HeaderLibraries])
	}
	for name, value := range got.Headers {
		if isCredentialHeader(name) {
			t.Errorf("credential header %q leaked to plugin", name)
		}
		if isEtherHeader(name) && name != HeaderViewer && name != HeaderViewerName && name != HeaderLibraries {
			t.Errorf("inbound %s leaked to plugin", name)
		}
		if strings.Contains(value, "tok1") || strings.Contains(value, "secret") {
			t.Errorf("credential value leaked in %s=%q", name, value)
		}
	}
}

// TestNoneLANUsesTrustedViewer proves the none_lan mode trusts only the
// host's LAN viewer resolver and never reads a credential.
func TestNoneLANUsesTrustedViewer(t *testing.T) {
	mgr := &fakeManager{handler: func(req plugins.Request) (plugins.Response, io.ReadCloser, error) {
		return plugins.Response{Status: 200}, io.NopCloser(strings.NewReader("ok")), nil
	}}
	decl := testDecl(core.LibraryMovies)
	decl.Meta.Auth = AuthNoneLAN
	f, err := New(decl, mgr)
	if err != nil {
		t.Fatal(err)
	}
	var seen *http.Request
	host := frontend.Host{LANViewer: func(_ context.Context, r *http.Request) (*core.Viewer, error) {
		seen = r
		return viewerWithLibraries(3, "Living room", core.LibraryMovies), nil
	}}
	inst, err := f.Mount(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	inst.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/opds/feed", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if seen == nil {
		t.Fatal("LAN viewer resolver not consulted")
	}
	if mgr.calls[0].Headers[HeaderViewer] != "3" {
		t.Fatalf("viewer header = %q", mgr.calls[0].Headers[HeaderViewer])
	}
}

// TestNoneLANRejectsWithoutResolver proves a none_lan adapter refuses to
// mount when the host cannot classify the LAN.
func TestNoneLANRejectsWithoutResolver(t *testing.T) {
	decl := testDecl(core.LibraryMovies)
	decl.Meta.Auth = AuthNoneLAN
	f, _ := New(decl, &fakeManager{})
	if _, err := f.Mount(context.Background(), frontend.Host{}); err == nil {
		t.Fatal("mounted without a LAN viewer resolver")
	}
}

// TestHiddenLibraryAnswers404 proves a viewer who cannot see the adapter's
// library gets 404, never 403, and the plugin is not called.
func TestHiddenLibraryAnswers404(t *testing.T) {
	viewer := viewerWithLibraries(7, "Alice", core.LibraryShows)
	mgr := &fakeManager{handler: func(req plugins.Request) (plugins.Response, io.ReadCloser, error) {
		return plugins.Response{Status: 200}, io.NopCloser(strings.NewReader("leak")), nil
	}}
	decl := testDecl(core.LibraryMovies)
	f, _ := New(decl, mgr)
	inst, err := f.Mount(context.Background(), testHost(map[string]*core.Viewer{"tok1": viewer}))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/opds/catalog", nil)
	req.Header.Set("Authorization", "Bearer tok1")
	rec := httptest.NewRecorder()
	inst.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if len(mgr.calls) != 0 {
		t.Fatalf("plugin was called for a hidden library")
	}
}

// TestMissingCredentialAnswers401 checks the unauthenticated path.
func TestMissingCredentialAnswers401(t *testing.T) {
	mgr := &fakeManager{handler: func(req plugins.Request) (plugins.Response, io.ReadCloser, error) {
		return plugins.Response{Status: 200}, io.NopCloser(strings.NewReader("")), nil
	}}
	f, _ := New(testDecl(core.LibraryMovies), mgr)
	inst, err := f.Mount(context.Background(), testHost(map[string]*core.Viewer{"tok1": viewerWithLibraries(7, "A", core.LibraryMovies)}))
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	inst.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/opds/catalog", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(mgr.calls) != 0 {
		t.Fatalf("plugin called without a credential")
	}
}

// TestHostedSmallResponseFallback proves a hosted (wasm) plugin that cannot
// stream still serves a small document through the buffered path.
func TestHostedSmallResponseFallback(t *testing.T) {
	mgr := &fakeManager{
		hosted: true,
		buff: func(req plugins.Request) (plugins.Response, error) {
			return plugins.Response{Status: 200, Headers: map[string]string{"Content-Type": "application/atom+xml"}, Body: []byte("<feed/>")}, nil
		},
	}
	f, _ := New(testDecl(core.LibraryMovies), mgr)
	inst, err := f.Mount(context.Background(), testHost(map[string]*core.Viewer{"tok1": viewerWithLibraries(7, "A", core.LibraryMovies)}))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/opds/feed", nil)
	req.Header.Set("Authorization", "Bearer tok1")
	rec := httptest.NewRecorder()
	inst.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != "<feed/>" {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
}

// TestHostedResponseCapRejectsLargeBody proves the hosted cap holds.
func TestHostedResponseCapRejectsLargeBody(t *testing.T) {
	mgr := &fakeManager{
		hosted: true,
		buff: func(req plugins.Request) (plugins.Response, error) {
			return plugins.Response{Status: 200, Body: make([]byte, MaxHostedResponseBytes+1)}, nil
		},
	}
	f, _ := New(testDecl(core.LibraryMovies), mgr)
	inst, _ := f.Mount(context.Background(), testHost(map[string]*core.Viewer{"tok1": viewerWithLibraries(7, "A", core.LibraryMovies)}))
	req := httptest.NewRequest(http.MethodGet, "/opds/big", nil)
	req.Header.Set("Authorization", "Bearer tok1")
	rec := httptest.NewRecorder()
	inst.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

// TestTicketExpiryAndScope proves a stream ticket is short-lived and bound
// to (viewer, item, adapter).
func TestTicketExpiryAndScope(t *testing.T) {
	issuer := NewTicketIssuer()
	now := time.Unix(1000, 0)
	issuer.now = func() time.Time { return now }
	issuer.SetTTL(30 * time.Second)

	token, err := issuer.Issue(StreamGrant{ViewerID: 7, ItemID: "book-1", Adapter: "feed"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issuer.Validate(token, 7, "book-1", "feed"); err != nil {
		t.Fatalf("valid ticket rejected: %v", err)
	}
	// Wrong viewer.
	if _, err := issuer.Validate(token, 8, "book-1", "feed"); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("wrong viewer accepted: %v", err)
	}
	// Wrong item.
	if _, err := issuer.Validate(token, 7, "book-2", "feed"); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("wrong item accepted: %v", err)
	}
	// Wrong adapter.
	if _, err := issuer.Validate(token, 7, "book-1", "other"); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("wrong adapter accepted: %v", err)
	}
	// Expiry.
	now = now.Add(31 * time.Second)
	if _, err := issuer.Validate(token, 7, "book-1", "feed"); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("expired ticket accepted: %v", err)
	}
}

// TestRegisterUnregisterLifecycleUnderMount proves the controller registers
// adapters, refuses to unregister while mounted, and cleans up on disable.
func TestRegisterUnregisterLifecycleUnderMount(t *testing.T) {
	reg := frontend.NewRegistry(func(frontend.Protocol) frontend.Host {
		return testHost(map[string]*core.Viewer{"tok1": viewerWithLibraries(7, "A", core.LibraryMovies)})
	})
	mgr := &fakeManager{handler: func(req plugins.Request) (plugins.Response, io.ReadCloser, error) {
		return plugins.Response{Status: 200}, io.NopCloser(strings.NewReader("ok")), nil
	}}
	cat := &fakeCatalog{decls: []Declaration{testDecl(core.LibraryMovies)}}
	ctrl, err := NewController(reg, mgr, cat)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if err := ctrl.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	id := AdapterID("plug1", "feed")
	if _, ok := reg.Instance(id); ok {
		t.Fatalf("adapter mounted before Enable")
	}
	if !registered(reg, id) {
		t.Fatalf("adapter not registered")
	}
	if err := ctrl.Enable(ctx, []Declaration{testDecl(core.LibraryMovies)}, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Instance(id); !ok {
		t.Fatalf("adapter not mounted")
	}
	// Registry refuses to unregister a mounted frontend.
	if err := reg.Unregister(id); !errors.Is(err, frontend.ErrAlreadyMounted) {
		t.Fatalf("Unregister while mounted = %v, want ErrAlreadyMounted", err)
	}
	// Disable unmounts and unregisters.
	if err := ctrl.Disable(ctx, "plug1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Instance(id); ok {
		t.Fatalf("adapter still mounted after Disable")
	}
	if registered(reg, id) {
		t.Fatalf("adapter still registered after Disable")
	}
	if len(ctrl.Live()) != 0 {
		t.Fatalf("controller still tracks adapters: %v", ctrl.Live())
	}
}

// registered reports whether the registry still holds the protocol.
func registered(reg *frontend.Registry, id frontend.Protocol) bool {
	for _, d := range reg.Descriptors() {
		if d.ID == id {
			return true
		}
	}
	return false
}

// fakeCatalog reports a fixed adapter set.
type fakeCatalog struct {
	decls []Declaration
}

func (c *fakeCatalog) EnabledAdapters(context.Context) ([]Declaration, error) {
	return c.decls, nil
}

// TestSyncReRegistersOnDeclarationChange proves a plugin upgrade that
// changes an adapter's declaration re-registers it under the same protocol.
func TestSyncReRegistersOnDeclarationChange(t *testing.T) {
	reg := frontend.NewRegistry(func(frontend.Protocol) frontend.Host {
		return testHost(map[string]*core.Viewer{"tok1": viewerWithLibraries(7, "A", core.LibraryMovies)})
	})
	mgr := &fakeManager{handler: func(req plugins.Request) (plugins.Response, io.ReadCloser, error) {
		return plugins.Response{Status: 200}, io.NopCloser(strings.NewReader("ok")), nil
	}}
	cat := &fakeCatalog{decls: []Declaration{testDecl(core.LibraryMovies)}}
	ctrl, err := NewController(reg, mgr, cat)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := ctrl.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	id := AdapterID("plug1", "feed")
	before := descriptorFor(reg, id)
	if before == nil {
		t.Fatal("adapter not registered")
	}
	// Change the declaration (new libraries) and re-sync.
	cat.decls = []Declaration{testDecl(core.LibraryShows)}
	if err := ctrl.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	after := descriptorFor(reg, id)
	if after == nil {
		t.Fatal("adapter lost on re-register")
	}
	// Same protocol id, re-registered (still exactly one descriptor).
	count := 0
	for _, d := range reg.Descriptors() {
		if d.ID == id {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("descriptors for %s = %d, want 1", id, count)
	}
}

func descriptorFor(reg *frontend.Registry, id frontend.Protocol) *frontend.Descriptor {
	for _, d := range reg.Descriptors() {
		if d.ID == id {
			dd := d
			return &dd
		}
	}
	return nil
}

// TestMediaHandlerRedeemsTicketAndServesRange proves the kind-media endpoint
// opens through KindStreams, honours Range, and refuses an expired ticket.
func TestMediaHandlerRedeemsTicketAndServesRange(t *testing.T) {
	issuer := NewTicketIssuer()
	now := time.Unix(2000, 0)
	issuer.now = func() time.Time { return now }
	issuer.SetTTL(30 * time.Second)

	var opened []string
	handler := NewMediaHandler(MediaDeps{
		Tickets: issuer,
		Viewer: func(_ context.Context, id int64) (*core.Viewer, error) {
			return viewerWithLibraries(id, "Alice", core.LibraryMovies), nil
		},
		Open: func(_ context.Context, _ *core.Viewer, ref string, accept []string) (*frontend.KindMedia, error) {
			opened = append(opened, ref)
			body := []byte("0123456789")
			return &frontend.KindMedia{
				ReaderAt: bytes.NewReader(body),
				Closer:   io.NopCloser(nil),
				Size:     int64(len(body)),
				MIME:     "application/epub+zip",
				Name:     "book",
			}, nil
		},
	})
	if handler == nil {
		t.Fatal("handler nil")
	}
	token, err := issuer.Issue(StreamGrant{ViewerID: 7, ItemID: "plug:kind:book-1", Adapter: "feed"})
	if err != nil {
		t.Fatal(err)
	}

	// Full request.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, KindMediaPath+token, nil))
	if rec.Code != 200 || rec.Body.String() != "0123456789" {
		t.Fatalf("full: status=%d body=%q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Errorf("Accept-Ranges = %q", rec.Header().Get("Accept-Ranges"))
	}
	if got := rec.Header().Get("Content-Type"); got != "application/epub+zip" {
		t.Errorf("Content-Type = %q", got)
	}

	// Ranged request.
	req := httptest.NewRequest(http.MethodGet, KindMediaPath+token, nil)
	req.Header.Set("Range", "bytes=2-5")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("range: status = %d, want 206", rec.Code)
	}
	if rec.Body.String() != "2345" {
		t.Errorf("range body = %q, want 2345", rec.Body.String())
	}
	if len(opened) != 2 || opened[0] != "plug:kind:book-1" {
		t.Fatalf("opened refs = %v", opened)
	}

	// Expired ticket is refused and opens nothing further.
	now = now.Add(31 * time.Second)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, KindMediaPath+token, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expired: status = %d, want 404", rec.Code)
	}
	if len(opened) != 2 {
		t.Fatalf("expired ticket opened an item")
	}
}

// TestMediaHandlerRejectsUnknownToken proves a bad token is a 404.
func TestMediaHandlerRejectsUnknownToken(t *testing.T) {
	handler := NewMediaHandler(MediaDeps{
		Tickets: NewTicketIssuer(),
		Viewer:  func(context.Context, int64) (*core.Viewer, error) { return &core.Viewer{ID: 1}, nil },
		Open: func(context.Context, *core.Viewer, string, []string) (*frontend.KindMedia, error) {
			t.Fatal("open called for unknown token")
			return nil, nil
		},
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, KindMediaPath+"nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
