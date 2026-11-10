package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/braedonsaunders/ether/internal/plugins/kit"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
	"github.com/braedonsaunders/ether/internal/store"
)

func newTestStudio(t *testing.T) (*Studio, *plugins.Manager, *[]string) {
	t.Helper()
	db := store.OpenTest(t)

	reg := extend.New()
	cat := tools.NewCatalogue()
	mgr, err := plugins.New(plugins.Deps{
		DB:       db,
		Registry: reg,
		Tools:    cat,
		// The production install check, so installs are tested as shipped.
		CheckPackage: kit.CheckInstall,
		// Hermetic: template previews that fetch real data fail fast offline.
		FetchClient: &http.Client{Transport: offlineTransport{}},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	eng := wasm.NewEngine(nil)
	t.Cleanup(func() { _ = eng.Close() })
	mgr.RegisterRuntime("hosted", plugins.HostedRuntimeFactory(eng))

	var events []string
	st := New(Deps{
		Manager: mgr,
		Engine:  eng,
		Publish: func(_ context.Context, event string, _ any) error {
			events = append(events, event)
			return nil
		},
	})
	return st, mgr, &events
}

func TestTemplatesAllValidAndRunnable(t *testing.T) {
	st, _, _ := newTestStudio(t)
	ctx := context.Background()

	tpls := Templates()
	if len(tpls) != 10 {
		t.Fatalf("expected 10 templates, got %d", len(tpls))
	}

	for _, tpl := range tpls {
		t.Run(tpl.ID, func(t *testing.T) {
			proj, err := st.CreateProject(ctx, CreateProjectOptions{
				Template: tpl.ID,
				Name:     "Sample " + tpl.Label,
			})
			if err != nil {
				t.Fatalf("create project for %s: %v", tpl.ID, err)
			}
			if len(proj.Diagnostics) > 0 {
				t.Fatalf("template %s produced initial diagnostics: %+v", tpl.ID, proj.Diagnostics)
			}

			report, err := st.CheckProject(ctx, proj.ID)
			if err != nil {
				t.Fatalf("check project %s: %v", tpl.ID, err)
			}
			if !report.OK {
				t.Fatalf("template %s failed check: %+v", tpl.ID, report)
			}

			runRes, err := st.RunProject(ctx, proj.ID, RunRequest{})
			if err != nil {
				t.Fatalf("run project %s: %v", tpl.ID, err)
			}
			if runRes.Status < 200 || runRes.Status >= 300 {
				t.Fatalf("template %s run status = %d, body = %s", tpl.ID, runRes.Status, string(runRes.Body))
			}

			prev, err := st.PreviewProject(ctx, proj.ID, "", "", nil)
			if err != nil {
				t.Fatalf("preview project %s: %v", tpl.ID, err)
			}
			if !prev.Valid {
				t.Fatalf("template %s preview invalid: %+v", tpl.ID, prev)
			}
		})
	}
}

func TestSaveFilesHotReloadAndDiagnostics(t *testing.T) {
	st, _, events := newTestStudio(t)
	ctx := context.Background()

	proj, err := st.CreateProject(ctx, CreateProjectOptions{
		Template: "channels",
		Name:     "Indie channels",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Introduce a TypeScript syntax error on line 4.
	badTS := "interface Req { method: string; path: string; }\n\nexport function handle(req: Req) {\n  const broken = ;\n  return { status: 200, body: {} };\n}\n"
	_, comp, err := st.SaveFiles(ctx, proj.ID, map[string]string{
		"src/plugin.ts": badTS,
	}, nil, nil)
	if err != nil {
		t.Fatalf("save files: %v", err)
	}
	if comp.OK {
		t.Fatalf("expected compile error for broken TS")
	}
	if len(comp.Diagnostics) == 0 {
		t.Fatalf("expected diagnostics, got none")
	}
	d := comp.Diagnostics[0]
	if d.File != "src/plugin.ts" || d.Line != 4 {
		t.Fatalf("unexpected diagnostic location: %+v", d)
	}

	// Fix the file and verify studio.reload SSE event fires.
	goodTS := "interface Req { method: string; path: string; }\nexport function handle(req: Req) {\n  return { status: 200, body: { streams: [{ name: \"Studio 4K\", url: \"https://streams.example.com/1.mkv\", quality: \"2160p\" }] } };\n}\n"
	_, comp2, err := st.SaveFiles(ctx, proj.ID, map[string]string{
		"src/plugin.ts": goodTS,
	}, nil, nil)
	if err != nil {
		t.Fatalf("save fixed files: %v", err)
	}
	if !comp2.OK {
		t.Fatalf("expected clean compile, got diagnostics: %+v", comp2.Diagnostics)
	}
	if len(*events) < 2 || (*events)[len(*events)-1] != "studio.reload" {
		t.Fatalf("expected studio.reload event, got %v", *events)
	}
}

func TestInspectorValidationHintsAndRedaction(t *testing.T) {
	call := plugins.CallRecord{
		ID:       1,
		Plugin:   "studio.channels",
		Route:    "/ether/streams",
		Method:   "GET",
		Status:   200,
		MS:       12,
		Request:  RedactJSON(json.RawMessage(`{"apiKey":"super-secret","imdbId":"tt0133093"}`)),
		Response: `{"streams":[{"name":"Main","quality":"2160p"}]}`,
	}
	if strings.Contains(call.Request, "super-secret") {
		t.Fatalf("expected apiKey to be redacted, got %s", call.Request)
	}

	hints := InspectCall(call)
	if len(hints) == 0 {
		t.Fatalf("expected validation hint for missing stream url/infoHash")
	}
	if !strings.Contains(hints[0].Message, "missing a url, an infoHash or a lazy id") {
		t.Fatalf("unexpected hint message: %+v", hints[0])
	}
}

func TestShadowModeAndUndo(t *testing.T) {
	st, _, _ := newTestStudio(t)
	ctx := context.Background()

	proj, err := st.CreateProject(ctx, CreateProjectOptions{
		Template: "list",
		Name:     "Watchlist sync",
	})
	if err != nil {
		t.Fatalf("create list project: %v", err)
	}

	// Run in shadow mode first.
	if _, err := st.SetShadow(ctx, proj.ID, true); err != nil {
		t.Fatalf("set shadow: %v", err)
	}
	if _, err := st.RunProject(ctx, proj.ID, RunRequest{
		Method: "POST",
		Path:   "/ether/jobs/sync",
	}); err != nil {
		t.Fatalf("run sync in shadow mode: %v", err)
	}

	shadowEntries, err := st.Journal(ctx, proj.ID, true)
	if err != nil {
		t.Fatalf("journal shadow: %v", err)
	}
	if len(shadowEntries) == 0 || !shadowEntries[0].Shadow {
		t.Fatalf("expected shadow journal entry, got %+v", shadowEntries)
	}

	// Disable shadow mode, run a live write, then undo.
	if _, err := st.SetShadow(ctx, proj.ID, false); err != nil {
		t.Fatalf("disable shadow: %v", err)
	}
	if _, err := st.RunProject(ctx, proj.ID, RunRequest{
		Method: "POST",
		Path:   "/ether/jobs/sync",
	}); err != nil {
		t.Fatalf("run sync live: %v", err)
	}

	undone, err := st.Undo(ctx, proj.ID)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if undone < 2 {
		t.Fatalf("expected at least 2 entries marked undone, got %d", undone)
	}
}

func TestSlotPrewiringAndHTTPRoutes(t *testing.T) {
	st, mgr, _ := newTestStudio(t)
	mux := st.WrapPluginHandler(mgr.Handler())

	// Create a project pre-wired to title.panel slot via HTTP POST /api/plugins/studio/projects.
	createBody := `{"template":"page","name":"Festival notes","slot":"title.panel"}`
	req := httptest.NewRequest(http.MethodPost, "/api/plugins/studio/projects", bytes.NewBufferString(createBody))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /projects status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Project Project `json:"project"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created project: %v", err)
	}
	if !strings.Contains(created.Project.Files["manifest.json"], `"slot": "title.panel"`) {
		t.Fatalf("expected manifest to contain slot title.panel, got %s", created.Project.Files["manifest.json"])
	}

	// Fetch view via /api/plugins/studio:{id}/views/title-panel (as PluginView does).
	viewReq := httptest.NewRequest(http.MethodGet, "/api/plugins/studio:"+created.Project.ID+"/views/title-panel", nil)
	viewRec := httptest.NewRecorder()
	mux.ServeHTTP(viewRec, viewReq)
	if viewRec.Code != http.StatusOK {
		t.Fatalf("GET dev view status = %d, body = %s", viewRec.Code, viewRec.Body.String())
	}

	// Preview install and install into manager.
	prevReq := httptest.NewRequest(http.MethodPost, "/api/plugins/studio/projects/"+created.Project.ID+"/install/preview", bytes.NewBufferString(`{}`))
	prevRec := httptest.NewRecorder()
	mux.ServeHTTP(prevRec, prevReq)
	if prevRec.Code != http.StatusOK {
		t.Fatalf("POST install/preview status = %d, body = %s", prevRec.Code, prevRec.Body.String())
	}

	instReq := httptest.NewRequest(http.MethodPost, "/api/plugins/studio/projects/"+created.Project.ID+"/install", bytes.NewBufferString(`{"shadow":true}`))
	instRec := httptest.NewRecorder()
	mux.ServeHTTP(instRec, instReq)
	if instRec.Code != http.StatusCreated {
		t.Fatalf("POST install status = %d, body = %s", instRec.Code, instRec.Body.String())
	}
}

func TestActionSlotScaffold(t *testing.T) {
	for _, tc := range []struct{ template, action string }{{"page", "createHighlight"}, {"list", "syncShelf"}, {"local_files", "matchFolder"}} {
		t.Run(tc.template, func(t *testing.T) {
			tpl, ok := LookupTemplate(tc.template)
			if !ok {
				t.Fatal("missing template")
			}
			_, _, files := applyProjectOptions(tpl, CreateProjectOptions{Slot: "actionMenu.title"})
			var manifest plugins.Manifest
			if err := json.Unmarshal([]byte(files["manifest.json"]), &manifest); err != nil {
				t.Fatal(err)
			}
			slots := manifest.Ether.Contributes.Slots
			if len(slots) != 1 || slots[0].Action != tc.action || slots[0].Route != "" {
				t.Fatalf("unexpected slots: %+v", slots)
			}
			if err := manifest.Validate(); err != nil {
				t.Fatal(err)
			}
			var stringsEN map[string]string
			if err := json.Unmarshal([]byte(files["i18n/en.json"]), &stringsEN); err != nil || stringsEN["slot_label"] == "" {
				t.Fatalf("missing slot label: %v", err)
			}
		})
	}
}

func TestDraftsSurviveRestartAndFailedSave(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	first := New(Deps{})
	defer first.Close()
	if err := first.OpenStorage(ctx, db); err != nil {
		t.Fatal(err)
	}
	p, err := first.CreateProject(ctx, CreateProjectOptions{Template: "page", Name: "Saved page"})
	if err != nil {
		t.Fatal(err)
	}
	draft := "export function handle() { const unfinished = ; }"
	if _, comp, err := first.SaveFiles(ctx, p.ID, map[string]string{"src/plugin.ts": draft}, nil, nil); err != nil || comp.OK {
		t.Fatalf("save invalid draft: %v ok=%v", err, comp.OK)
	}
	second := New(Deps{})
	defer second.Close()
	if err := second.OpenStorage(ctx, db); err != nil {
		t.Fatal(err)
	}
	restored, err := second.GetProject(ctx, p.ID)
	if err != nil || restored.Files["src/plugin.ts"] != draft {
		t.Fatalf("lost draft: %v", err)
	}
	duplicate, err := second.CreateProject(ctx, CreateProjectOptions{Template: "page", Name: "Saved page"})
	if err != nil || duplicate.ID == p.ID {
		t.Fatalf("project identity collision: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := second.SaveFiles(cancelled, p.ID, map[string]string{"src/plugin.ts": "changed"}, nil, nil); err == nil {
		t.Fatal("failed persistence reported success")
	}
	restored, err = second.GetProject(ctx, p.ID)
	if err != nil || restored.Files["src/plugin.ts"] != draft {
		t.Fatal("failed save mutated published draft")
	}
	if err = second.DeleteProject(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	third := New(Deps{})
	defer third.Close()
	if err = third.OpenStorage(ctx, db); err != nil {
		t.Fatal(err)
	}
	for _, summary := range third.ListProjects(ctx) {
		if summary.ID == p.ID {
			t.Fatal("deleted draft restored")
		}
	}
	if len(third.ListProjects(ctx)) != 1 {
		t.Fatal("deleting one draft affected another")
	}

}

func TestMountedStudioRoute(t *testing.T) {
	s, _, _ := newTestStudio(t)
	w := httptest.NewRecorder()
	s.WrapPluginHandler(nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/studio/projects", nil))
	if w.Code != 200 {
		t.Fatalf("mounted route returned %d: %s", w.Code, w.Body.String())
	}
}

// A Studio draft installs as a working plugin: the installed runtime loads
// the bundle Studio built instead of bundling it a second time.
func TestInstalledDraftRunsItsHandler(t *testing.T) {
	s, mgr, _ := newTestStudio(t)
	ctx := context.Background()
	p, err := s.CreateProject(ctx, CreateProjectOptions{Template: "page", Name: "Install check"})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := s.InstallProject(ctx, p.ID, plugins.InstallOptions{})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	src, err := s.buildInstallSource(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	man, _ := plugins.ParseManifest(src.Manifest)
	if man.Ether.Runtime.Entry != installBundlePath || len(src.Files[installBundlePath]) == 0 {
		t.Fatalf("install entry %q, bundle %d bytes", man.Ether.Runtime.Entry, len(src.Files[installBundlePath]))
	}
	resp, err := mgr.Call(ctx, rec.ID, plugins.Request{Method: http.MethodGet, Path: "/ether/health"})
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("installed draft did not answer: %v %+v", err, resp)
	}
	// Its page text arrives resolved, not as "$t:" keys.
	w := httptest.NewRecorder()
	mgr.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/"+rec.ID+"/views/overview?locale=en", nil))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "$t:") || !strings.Contains(w.Body.String(), "Featured picks") {
		t.Fatalf("installed view: %d %s", w.Code, w.Body.String())
	}
}

// A remix's add-on link can hold a debrid key: it must not land in the
// draft's source or its stored record.
func TestRemixDoesNotPersistAddonLink(t *testing.T) {
	s, _, _ := newTestStudio(t)
	ctx := context.Background()
	link := "https://addon.example/realdebrid=SECRETKEY123/manifest.json"
	p, err := s.CreateProject(ctx, CreateProjectOptions{Template: "stremio_remix", Name: "Remix", StremioURL: link})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "SECRETKEY123") {
		t.Fatalf("add-on key persisted with the draft")
	}
	if !strings.Contains(p.Files["src/plugin.ts"], "addon.example") {
		t.Fatal("upstream host not noted in source")
	}
}

// Every draft is its own plugin, named after the draft, so installing a
// second one never replaces the first.
func TestDraftsGetTheirOwnPluginIDs(t *testing.T) {
	s, _, _ := newTestStudio(t)
	ctx := context.Background()
	a, err := s.CreateProject(ctx, CreateProjectOptions{Template: "page", Name: "Friday screenings"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.InstallProject(ctx, a.ID, plugins.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProject(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateProject(ctx, CreateProjectOptions{Template: "page", Name: "Friday screenings"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateProject(ctx, CreateProjectOptions{Template: "page", Name: "Weekend picks!"})
	if err != nil {
		t.Fatal(err)
	}
	if a.PluginID != "community.friday-screenings" || b.PluginID != "community.friday-screenings-2" || c.PluginID != "community.weekend-picks" {
		t.Fatalf("ids: %s %s %s", a.PluginID, b.PluginID, c.PluginID)
	}
}

// A draft's preview reaches only the hosts its manifest declares.
func TestPreviewFetchHonoursDeclaredNetwork(t *testing.T) {
	_, mgr, _ := newTestStudio(t)
	_, err := mgr.PreviewFetch(context.Background(), []string{"api.example.org"}, plugins.FetchRequest{URL: "https://elsewhere.example.net/"})
	if err == nil || !strings.Contains(err.Error(), "network permissions") {
		t.Fatalf("undeclared host allowed: %v", err)
	}
	if _, err := mgr.PreviewFetch(context.Background(), []string{"*"}, plugins.FetchRequest{URL: "file:///etc/passwd"}); err == nil {
		t.Fatal("non-http scheme allowed")
	}
}

type offlineTransport struct{}

func (offlineTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("offline in tests")
}

// The showcase templates install as they come, even offline: a draft the
// owner has not touched must never fail the install check.
func TestNativeTemplatesPassInstallCheck(t *testing.T) {
	s, _, _ := newTestStudio(t)
	ctx := context.Background()
	for _, tpl := range []string{"channels", "video_source"} {
		p, err := s.CreateProject(ctx, CreateProjectOptions{Template: tpl, Name: "Check " + tpl})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.PreviewInstallProject(ctx, p.ID); err != nil {
			t.Errorf("%s install check: %v", tpl, err)
		}
	}
}
