package plugins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func weatherFixture(t *testing.T) (Manifest, map[string][]byte) {
	t.Helper()
	raw, e := os.ReadFile("../../examples/onair-weather/manifest.json")
	if e != nil {
		t.Fatal(e)
	}
	m, e := ParseManifest(raw)
	if e != nil {
		t.Fatal(e)
	}
	files := map[string][]byte{}
	err := filepath.WalkDir("../../examples/onair-weather", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel("../../examples/onair-weather", path)
		files[filepath.ToSlash(rel)], err = os.ReadFile(path)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	return m, files
}
func TestOnAirManifestContract(t *testing.T) {
	m, files := weatherFixture(t)
	if e := m.Validate(); e != nil {
		t.Fatal(e)
	}
	if e := validateSegmentFiles(m, files); e != nil {
		t.Fatal(e)
	}
	cases := []struct {
		name string
		mut  func(*Manifest)
	}{
		{"remote", func(m *Manifest) { m.Ether.Runtime = RuntimeDecl{Kind: "remote"} }},
		{"kind", func(m *Manifest) { m.Ether.Kind = KindIntegration }},
		{"duration", func(m *Manifest) { m.Ether.Contributes.OnAirSegments[0].Duration = 13 }},
		{"escape", func(m *Manifest) { m.Ether.Contributes.OnAirSegments[0].File = "../scene.json" }},
		{"feed", func(m *Manifest) { m.Ether.Contributes.OnAirSegments[0].Feeds = []string{"missing"} }},
		{"field", func(m *Manifest) { m.Ether.Contributes.OnAirFeeds[0].Fields["temp"] = "video" }},
		{"age", func(m *Manifest) { m.Ether.Contributes.OnAirFeeds[0].MaxAge = "bad" }},
		{"schedule", func(m *Manifest) { m.Ether.Schedule[0].Every = "1m" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := weatherFixture(t)
			tc.mut(&m)
			if m.Validate() == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	files["segments/forecast.json"] = []byte(`{"v":1,"moment":"plugin:community.example.weather/forecast","duration":120,"poster":0,"layers":[{"id":"bad","type":"script"}]}`)
	if validateSegmentFiles(m, files) == nil {
		t.Fatal("unsafe document accepted")
	}
}
func TestOnAirFeedFieldBounds(t *testing.T) {
	f := OnAirFeed{Fields: map[string]string{"text": "text", "number": "number", "list": "list", "image": "image"}}
	cases := map[string]any{"text": "<b>markup</b>", "number": "12", "list": make([]any, 51), "image": "not-an-image", "extra": true}
	for field, bad := range cases {
		t.Run(field, func(t *testing.T) {
			data := map[string]any{"text": "Forecast", "number": 12, "list": []any{}, "image": "bad"}
			data[field] = bad
			raw, _ := json.Marshal(data)
			_, _, err := ValidateFeed(f, raw, nil)
			if !hasField(err, field) {
				t.Fatalf("missing field error: %v", err)
			}
		})
	}
	f = OnAirFeed{Fields: map[string]string{"text": "text"}}
	raw, _ := json.Marshal(map[string]any{"text": strings.Repeat("a", 4097)})
	if _, _, e := ValidateFeed(f, raw, nil); !hasField(e, "text") {
		t.Fatal(e)
	}
}
func TestOnAirLeaderLastGoodAndExpiry(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	m, files := weatherFixture(t)
	raw, _ := json.Marshal(m)
	p, e := env.m.PreviewInstall(ctx, Source{Kind: "studio", Manifest: raw, Files: files})
	if e != nil {
		t.Fatal(e)
	}
	rec, e := env.m.Install(ctx, p, InstallOptions{})
	if e != nil {
		t.Fatal(e)
	}
	env.fake.routes["/ether/feed/"] = func(req Request) (Response, error) {
		if req.Op != "feed" || (req.ID != "now" && req.ID != "daily") {
			t.Fatal(req)
		}
		for _, feed := range m.Ether.Contributes.OnAirFeeds {
			if feed.ID == req.ID {
				body, _ := json.Marshal(feed.Sample)
				return Response{Status: 200, Body: body}, nil
			}
		}
		return Response{}, nil
	}
	now := time.Now().UTC()
	env.lead = false
	env.m.refreshOnAir(ctx, []Record{rec}, now)
	var count int
	_ = env.m.store.db.QueryRow("SELECT COUNT(*) FROM onair_feed_values").Scan(&count)
	if count != 0 {
		t.Fatal("follower fetched")
	}
	env.lead = true
	env.m.refreshOnAir(ctx, []Record{rec}, now)
	first, ok := env.m.PluginSegment("plugin:" + m.ID + "/forecast")
	if !ok || first.Slots["now.temp"] != "18°C" || !first.Expires.After(now) {
		t.Fatal(first)
	}
	env.fake.routes["/ether/feed/"] = func(Request) (Response, error) {
		return Response{Status: 200, Body: json.RawMessage(`{"temp":"invalid","summary":"Changed"}`)}, nil
	}
	env.m.refreshOnAir(ctx, []Record{rec}, now.Add(20*time.Minute))
	last, _ := env.m.PluginSegment("plugin:" + m.ID + "/forecast")
	if first.Version != last.Version || last.Slots["now.temp"] != "18°C" {
		t.Fatal("failed refresh replaced good data")
	}
	env.m.refreshOnAir(ctx, []Record{rec}, now.Add(2*time.Hour))
	last, _ = env.m.PluginSegment("plugin:" + m.ID + "/forecast")
	if last.Expires.After(now.Add(2 * time.Hour)) {
		t.Fatal("stale value renewed")
	}
}
