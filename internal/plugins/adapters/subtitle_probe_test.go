package adapters_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/adapters"
)

func TestSubtitleProbeExplicitPickIsolationAndRevocation(t *testing.T) {
	h := newListTestHarness(t)
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	probe := adapters.NewSubtitleProbe(h.mgr, h.cat)
	defer probe.Close()
	man := plugins.Manifest{ID: "community.probe.subtitles", Version: "1.0.0", Name: "Subtitle test", Ether: &plugins.EtherBlock{Protocol: 1, Kind: plugins.KindExtension, Summary: "Test timed subtitle text", Runtime: plugins.RuntimeDecl{Kind: "hosted", Entry: "plugin.js"}, Capabilities: []plugins.Capability{plugins.CapSubtitles}, Contributes: plugins.Contributes{Subtitles: &plugins.SubtitlesMeta{Languages: json.RawMessage(`["en"]`)}}}}
	raw, _ := json.Marshal(man)
	preview, err := h.mgr.PreviewInstall(context.Background(), plugins.Source{Kind: "studio", Manifest: raw, Files: map[string][]byte{"plugin.js": []byte("// fixture")}})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := h.mgr.Install(context.Background(), preview, plugins.InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var downloads atomic.Int64
	h.rt.routes["/subtitles/"] = func(plugins.Request) (plugins.Response, error) {
		return plugins.Response{Status: 200, Body: json.RawMessage(`{"subtitles":[{"id":"one","lang":"en","format":"srt"}]}`)}, nil
	}
	h.rt.routes["/ether/subtitles/download"] = func(req plugins.Request) (plugins.Response, error) {
		downloads.Add(1)
		if req.Headers["X-Ether-Pick"] == "" {
			t.Error("download without explicit pick authorization")
		}
		return plugins.Response{Status: 200, Body: json.RawMessage(`{"format":"srt","encoding":"utf8","data":"1\n00:00:01,000 --> 00:00:02,000\nA test cue\n"}`)}, nil
	}
	handler := probe.Handler()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	search := func() (string, string) {
		w := call("POST", "/subtitles/search", `{"plugin":"`+rec.ID+`","key":"`+core.MovieKey(101).String()+`","language":"en"}`)
		if w.Code != 200 {
			t.Fatalf("search %d %s", w.Code, w.Body)
		}
		if !strings.Contains(w.Body.String(), "Subtitle one.srt") {
			t.Fatalf("missing stable fallback label: %s", w.Body)
		}
		var result struct {
			Session string `json:"session"`
			Offers  []struct {
				ID string `json:"id"`
			} `json:"offers"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Offers) != 1 {
			t.Fatalf("offers %s %v", w.Body, err)
		}
		return result.Session, result.Offers[0].ID
	}
	session, _ := search()
	if downloads.Load() != 0 {
		t.Fatal("listing downloaded a subtitle")
	}
	w := call("POST", "/subtitles/"+session+"/pick", `{"offer":"999"}`)
	if w.Code == 200 || downloads.Load() != 0 {
		t.Fatalf("unoffered pick accepted: %d %s", w.Code, w.Body)
	}
	session, offer := search()
	w = call("POST", "/subtitles/"+session+"/pick", `{"offer":"`+offer+`"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"cues":1`) || !strings.Contains(w.Body.String(), `"cleaned":true`) || downloads.Load() != 1 {
		t.Fatalf("pick %d %s", w.Code, w.Body)
	}
	if w = call("POST", "/subtitles/"+session+"/pick", `{"offer":"`+offer+`"}`); w.Code != 404 {
		t.Fatal("consumed test reused")
	}
	session, offer = search()
	if err := h.mgr.SetEnabled(context.Background(), rec.ID, false); err != nil {
		t.Fatal(err)
	}
	if w = call("POST", "/subtitles/"+session+"/pick", `{"offer":"`+offer+`"}`); w.Code == 200 || downloads.Load() != 1 {
		t.Fatal("revoked offer downloaded")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary test files remain: %v %v", entries, err)
	}
}
