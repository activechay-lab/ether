package adapters_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/markers"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/adapters"
)

func TestMarkerPluginBackgroundAndLifecycle(t *testing.T) {
	h := newListTestHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := extend.PluginID("community.marker.test", "markers")
	svc := markers.New(markers.Options{DB: h.db, Config: func() markers.Lookup { return markers.Lookup{SourceOrder: []string{source, "chapter", "theintrodb"}} }})
	bridge := adapters.AttachMarkers(ctx, h.mgr, svc, h.cat, adapters.MarkerBridgeOptions{Selected: func(id string) bool { return id == source }})
	t.Cleanup(func() { cancel(); bridge.Wait() })
	man := map[string]any{"id": "community.marker.test", "version": "1.0.0", "name": "Test markers", "ether": map[string]any{"protocol": 1, "kind": "extension", "summary": "Marker provider for testing", "runtime": map[string]any{"kind": "hosted", "entry": "dist/plugin.js"}, "capabilities": []string{"markers"}, "contributes": map[string]any{"markers": map[string]any{"kinds": []string{"intro", "preview"}}}}}
	raw, _ := json.Marshal(man)
	p, err := h.mgr.PreviewInstall(ctx, plugins.Source{Kind: "studio", Manifest: raw, Files: map[string][]byte{"dist/plugin.js": []byte("// fixture")}})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := h.mgr.Install(ctx, p, plugins.InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}
	h.rt.routes["/ether/markers/"] = func(_ plugins.Request) (plugins.Response, error) {
		return plugins.Response{Status: 200, Body: json.RawMessage(`{"markers":[{"kind":"intro","start":10,"end":60},{"kind":"preview","start":90,"end":95}]}`)}, nil
	}
	key := core.MovieKey(101)
	info := core.MediaInfo{Duration: 120 * time.Second}
	before, err := svc.CachedMarkers(ctx, key, info)
	if err != nil || len(before) != 0 {
		t.Fatalf("before: %+v %v", before, err)
	}
	<-bridge.Schedule(key, info.Duration)
	got, err := svc.CachedMarkers(ctx, key, info)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Type != "preview" {
		t.Fatalf("markers: %+v", got)
	}
	if err := h.mgr.SetEnabled(ctx, rec.ID, false); err != nil {
		t.Fatal(err)
	}
	got, err = svc.CachedMarkers(ctx, key, info)
	if err != nil || len(got) != 0 {
		t.Fatalf("disabled markers: %+v %v", got, err)
	}
}
