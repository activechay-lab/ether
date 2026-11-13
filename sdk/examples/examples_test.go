package examples_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/kit"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
)

type fixtureHost struct {
	plugins.Host
	kv                   map[string][]byte
	folders              map[string][]plugins.FolderEntry
	channel, guide, boot []byte
	settings             map[string]any
}

func (h *fixtureHost) Settings(_ context.Context) (map[string]any, error) {
	if h.settings != nil {
		return h.settings, nil
	}
	if h.folders != nil {
		return map[string]any{"folder": "/media"}, nil
	}
	return map[string]any{}, nil
}
func (h *fixtureHost) KVGet(_ context.Context, k string) ([]byte, bool, error) {
	v, ok := h.kv[k]
	return v, ok, nil
}
func (h *fixtureHost) KVPut(_ context.Context, k string, v []byte) error { h.kv[k] = v; return nil }
func (h *fixtureHost) KVDelete(_ context.Context, k string) error        { delete(h.kv, k); return nil }
func (h *fixtureHost) KVList(_ context.Context, p string) ([]string, error) {
	var out []string
	for k := range h.kv {
		if strings.HasPrefix(k, p) {
			out = append(out, k)
		}
	}
	return out, nil
}
func (h *fixtureHost) FoldersList(_ context.Context, root, path string) ([]plugins.FolderEntry, error) {
	if path != "" {
		root += "/" + path
	}
	return h.folders[root], nil
}
func (h *fixtureHost) FoldersStat(_ context.Context, root, path string) (plugins.FolderEntry, error) {
	return plugins.FolderEntry{Path: path}, nil
}
func (h *fixtureHost) CatalogSearch(_ context.Context, q string, _, _ int) (json.RawMessage, error) {
	if q == "Example Movie" {
		return json.RawMessage(`{"titles":[{"id":1,"tmdbId":10,"title":"Example Movie","type":"movie","year":2001}]}`), nil
	}
	return json.RawMessage(`{"titles":[]}`), nil
}
func (h *fixtureHost) HTTPFetch(_ context.Context, r plugins.FetchRequest) (plugins.FetchResponse, error) {
	v := h.guide
	if strings.Contains(r.URL, "boot.pluto.tv") {
		v = h.boot
	}
	if strings.Contains(r.URL, "channels.json") {
		v = h.channel
	}
	return plugins.FetchResponse{Status: 200, Body: v}, nil
}
func (h *fixtureHost) Log(context.Context, string, string, map[string]any) error { return nil }
func fixture(t *testing.T, p string) []byte {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestExamplesPackAndRunHosted(t *testing.T) {
	for _, name := range []string{"local-folders", "pluto-tv"} {
		t.Run(name, func(t *testing.T) {
			pkg, err := kit.LoadDir(name)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := kit.Pack(pkg)
			if err != nil {
				t.Fatal(err)
			}
			packed, err := kit.LoadPkgBytes(raw)
			if err != nil {
				t.Fatal(err)
			}
			if packed.Manifest.Ether.Runtime.Entry != "dist/plugin.js" {
				t.Fatal("SDK entry was not bundled")
			}
			h := &fixtureHost{kv: map[string][]byte{}}
			if name == "local-folders" {
				if err = json.Unmarshal(fixture(t, "local-folders/tests/fixtures/folders.json"), &h.folders); err != nil {
					t.Fatal(err)
				}
			} else {
				h.channel = fixture(t, "pluto-tv/tests/fixtures/channels.json")
				h.guide = fixture(t, "pluto-tv/tests/fixtures/guide.json")
				h.boot = fixture(t, "pluto-tv/tests/fixtures/boot.json")
			}
			engine := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
			defer engine.Close()
			rt, err := plugins.HostedRuntimeFactory(engine)(plugins.Installed{ID: packed.Manifest.ID, Manifest: packed.Manifest, Host: h, Files: func(p string) ([]byte, bool) { v, ok := packed.Files[p]; return v, ok }})
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close()
			call := func(method, path, body string) plugins.Response {
				t.Helper()
				r, e := rt.Call(context.Background(), plugins.Request{Method: method, Path: path, Body: json.RawMessage(body)})
				if e != nil || r.Status != 200 {
					t.Fatalf("%s: %d %s %v", path, r.Status, r.Body, e)
				}
				return r
			}
			if name == "local-folders" {
				call("POST", "/ether/configure", `{"folder":"/media"}`)
				call("POST", "/ether/job/scan", `{}`)
				r := call("GET", "/stream/movie/tmdb%3A10.json", "")
				if !strings.Contains(string(r.Body), "Example.Movie.2001.1080p.mkv") {
					t.Fatalf("stream: %s", r.Body)
				}
			} else {
				r := call("GET", "/ether/channels.json", "")
				if !strings.Contains(string(r.Body), "Bloomberg TV+") {
					t.Fatalf("channels: %s", r.Body)
				}
				r = call("GET", "/ether/guide/62b976b93279cb000772c6f9.json", "")
				if !strings.Contains(string(r.Body), "Bloomberg TV+") {
					t.Fatalf("guide: %s", r.Body)
				}
				r = call("GET", "/stream/tv/62b976b93279cb000772c6f9.json", "")
				if !strings.Contains(string(r.Body), "fixture-token") {
					t.Fatalf("stream: %s", r.Body)
				}
			}
		})
	}
}

func TestStudioExamplesPackAndConform(t *testing.T) {
	for _, name := range []string{"tvmaze", "october-after-dark", "canada-live", "easydebrid", "travel-mode", "movie-night"} {
		t.Run(name, func(t *testing.T) {
			pkg, err := kit.LoadDir(name)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := kit.Pack(pkg)
			if err != nil {
				t.Fatal(err)
			}
			packed, err := kit.LoadPkgBytes(raw)
			if err != nil {
				t.Fatal(err)
			}
			report := kit.Check(context.Background(), packed)
			if !report.OK {
				t.Fatalf("install conformance: %+v", report.Errors)
			}
		})
	}
}

func (fixtureHost) FoldersHash(context.Context, string, string) (plugins.FolderHash, error) {
	return plugins.FolderHash{}, nil
}

func (fixtureHost) FoldersHashStart(context.Context, string, string) (plugins.FolderHashJob, error) {
	return plugins.FolderHashJob{}, nil
}

func (fixtureHost) FoldersHashPoll(context.Context, string) (plugins.FolderHashJob, error) {
	return plugins.FolderHashJob{}, nil
}

func (fixtureHost) FoldersHashCancel(context.Context, string) (plugins.FolderHashJob, error) {
	return plugins.FolderHashJob{}, nil
}
