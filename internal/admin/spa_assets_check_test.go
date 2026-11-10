package admin

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func TestOwnerUIAndEmbeddedAssetsRemainAvailable(t *testing.T) {
	t.Run("built fixture", func(t *testing.T) {
		ui := fstest.MapFS{
			"index.html":        &fstest.MapFile{Data: []byte(`<!doctype html><div id="root"></div>`)},
			"assets/app-123.js": &fstest.MapFile{Data: []byte(`console.log("ether");`)},
		}
		h := &Handler{ui: ui}
		for _, path := range []string{"/", "/settings/providers", "/assets/app-123.js"} {
			w := httptest.NewRecorder()
			h.handleSPA(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusOK || w.Body.Len() == 0 {
				t.Fatalf("%s: status %d, body length %d", path, w.Code, w.Body.Len())
			}
			if strings.HasPrefix(path, "/assets/") {
				if !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
					t.Errorf("asset cache header: %q", w.Header().Get("Cache-Control"))
				}
			} else if !strings.Contains(w.Body.String(), `id="root"`) {
				t.Errorf("%s: expected SPA shell, got %q", path, w.Body.String())
			}
		}
	})

	t.Run("unbuilt placeholder", func(t *testing.T) {
		h := &Handler{ui: fstest.MapFS{".gitkeep": &fstest.MapFile{}}}
		w := httptest.NewRecorder()
		h.handleSPA(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("Content-Type = %q, want text/html", ct)
		}
		if !strings.Contains(w.Body.String(), "Ether console is not built") {
			t.Errorf("unexpected placeholder body: %q", w.Body.String())
		}

		head := httptest.NewRecorder()
		h.handleSPA(head, httptest.NewRequest(http.MethodHead, "/", nil))
		if head.Code != http.StatusServiceUnavailable || head.Body.Len() != 0 {
			t.Errorf("HEAD /: status %d, body length %d", head.Code, head.Body.Len())
		}
	})

	t.Run("embedded dist", func(t *testing.T) {
		if os.Getenv("ETHER_REQUIRE_BUILT_UI") == "1" && !ConsoleBuilt() {
			t.Fatal("ETHER_REQUIRE_BUILT_UI=1, but internal/admin/dist/index.html is not built")
		}
		if !ConsoleBuilt() {
			for name, handler := range map[string]http.Handler{"handler": http.HandlerFunc((&Handler{}).handleSPA), "shell": Shell()} {
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ether/", nil))
				if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "Ether console is not built") {
					t.Fatalf("%s unbuilt /ether/: status %d body %q", name, w.Code, w.Body.String())
				}
			}
			return
		}
		var asset string
		err := fs.WalkDir(distFS, "dist/assets", func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && asset == "" {
				asset = strings.TrimPrefix(name, "dist/")
			}
			return nil
		})
		if err != nil || asset == "" {
			t.Fatalf("embedded assets unavailable: %v", err)
		}
		h := &Handler{}
		for _, path := range []string{"/", "/" + asset} {
			w := httptest.NewRecorder()
			h.handleSPA(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusOK || w.Body.Len() == 0 {
				t.Errorf("%s: status %d, body length %d", path, w.Code, w.Body.Len())
			}
			if path != "/" && !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
				t.Errorf("asset cache header: %q", w.Header().Get("Cache-Control"))
			}
		}
		shellRec := httptest.NewRecorder()
		Shell().ServeHTTP(shellRec, httptest.NewRequest(http.MethodGet, "/ether/"+asset, nil))
		if shellRec.Code != http.StatusOK || !strings.Contains(shellRec.Header().Get("Cache-Control"), "immutable") {
			t.Errorf("Shell() asset %s: status %d cache %q", asset, shellRec.Code, shellRec.Header().Get("Cache-Control"))
		}
	})
}
