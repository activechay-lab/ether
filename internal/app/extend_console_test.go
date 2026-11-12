package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/braedonsaunders/ether/internal/extend"
)

func TestConsoleDescriptorsRegister(t *testing.T) {
	r := extend.New()
	if err := (&App{}).registerConsoleDescriptors(r); err != nil {
		t.Fatal(err)
	}
	pages := r.List(extend.KindPage)
	if len(pages) != 24 {
		t.Fatalf("pages = %d, want 24", len(pages))
	}
	// Sidebar order and grouping, exactly as the old navigation() list.
	wantNav := []struct{ id, group, route string }{
		{"overview", "", "/"}, {"activity", "", "/activity"}, {"viewers", "", "/viewers"},
		{"libraries", "library", "/libraries"}, {"collections", "library", "/collections"},
		{"home", "library", "/home"}, {"livetv", "library", "/livetv"},
		{"calendar", "library", "/calendar"}, {"metadata", "library", "/metadata"},
		{"sources", "streaming", "/sources"}, {"releases", "streaming", "/quality"},
		{"cache", "streaming", "/cache"}, {"playback", "streaming", "/playback"},
		{"general", "server", "/server"}, {"apps", "server", "/apps"},
		{"integrations", "server", "/services"}, {"plugins", "server", "/plugins"},
		{"notifications", "server", "/notifications"}, {"cluster", "server", "/cluster"},
		{"maintenance", "server", "/maintenance"},
	}
	// Registry order is group, then order: "" < "library" < "server" <
	// "streaming", so compare per group.
	nav := r.List(extend.KindNavItem)
	if len(nav) != len(wantNav) {
		t.Fatalf("navItems = %d, want %d", len(nav), len(wantNav))
	}
	byID := map[string]extend.Descriptor{}
	for _, d := range nav {
		byID[d.ID] = d
	}
	for group, ids := range map[string][]string{
		"":          {"overview", "activity", "viewers"},
		"library":   {"libraries", "collections", "home", "livetv", "calendar", "metadata"},
		"streaming": {"sources", "playback", "releases", "cache"},
		"server":    {"general", "apps", "integrations", "plugins", "notifications", "cluster", "maintenance"},
	} {
		var got []string
		for _, d := range nav {
			if d.Group == group {
				got = append(got, d.ID)
			}
		}
		if len(got) != len(ids) {
			t.Fatalf("group %q = %v, want %v", group, got, ids)
		}
		for i := range ids {
			if got[i] != ids[i] {
				t.Fatalf("group %q = %v, want %v", group, got, ids)
			}
		}
	}
	for _, w := range wantNav {
		d, ok := byID[w.id]
		if !ok {
			t.Fatalf("missing navItem %s", w.id)
		}
		meta, err := extend.MetaOf[extend.NavItemMeta](d)
		if err != nil || meta.Route != w.route || d.Group != w.group {
			t.Errorf("navItem %s: meta=%+v group=%q err=%v", w.id, meta, d.Group, err)
		}
		if d.Icon == "" {
			t.Errorf("navItem %s: no icon", w.id)
		}
	}
	// Every destination href is unique across pages and sections.
	seen := map[string]bool{}
	for _, p := range pages {
		meta, err := extend.MetaOf[extend.PageMeta](p)
		if err != nil {
			t.Fatal(err)
		}
		if seen[meta.Route] {
			t.Errorf("duplicate route %s", meta.Route)
		}
		seen[meta.Route] = true
		for _, s := range meta.Sections {
			href := meta.Route + "/" + s.Key
			if seen[href] {
				t.Errorf("duplicate destination %s", href)
			}
			seen[href] = true
		}
	}
	// Commands: 7 ⌘K actions with routes, plus agent suggestions.
	var actions, suggestions int
	for _, c := range r.List(extend.KindCommand) {
		meta, err := extend.MetaOf[extend.CommandMeta](c)
		if err != nil {
			t.Fatal(err)
		}
		if meta.AgentSuggestion {
			suggestions++
			if len(meta.RoutePatterns) == 0 {
				t.Errorf("suggestion %s has no route patterns", c.ID)
			}
		} else {
			actions++
			if meta.Route == "" {
				t.Errorf("command %s has no route", c.ID)
			}
		}
	}
	if actions != 7 || suggestions != 4 {
		t.Errorf("commands: actions=%d suggestions=%d, want 7 and 4", actions, suggestions)
	}
	integrations := r.List(extend.KindIntegration)
	if len(integrations) != 8 {
		t.Fatalf("integrations = %d, want 8", len(integrations))
	}
	for _, d := range integrations {
		meta, err := extend.MetaOf[extend.IntegrationMeta](d)
		if err != nil {
			t.Fatal(err)
		}
		if meta.Category == "" || meta.Category != d.Group {
			t.Errorf("integration %s: category %q != group %q", d.ID, meta.Category, d.Group)
		}
	}
}

// TestConsoleDescriptorFixtureMatchesMock keeps the console mock backend in
// step with Go: web/src/mock/extend/descriptors/console.json is generated
// from these descriptors. Regenerate with ETHER_UPDATE_FIXTURES=1.
func TestConsoleDescriptorFixtureMatchesMock(t *testing.T) {
	r := extend.New()
	if err := (&App{}).registerConsoleDescriptors(r); err != nil {
		t.Fatal(err)
	}
	ds := r.List(extend.KindPage, extend.KindNavItem, extend.KindCommand, extend.KindIntegration)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(ds); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "web", "src", "mock", "extend", "descriptors", "console.json")
	if os.Getenv("ETHER_UPDATE_FIXTURES") == "1" {
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read mock fixture: %v (regenerate with ETHER_UPDATE_FIXTURES=1)", err)
	}
	if !bytes.Equal(saved, buf.Bytes()) {
		t.Fatal("web mock console descriptors drifted from Go; regenerate with ETHER_UPDATE_FIXTURES=1 go test ./internal/app -run TestConsoleDescriptorFixtureMatchesMock")
	}
}
