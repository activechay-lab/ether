package remote

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/plugins"
)

// A plain Stremio add-on published as static files in a GitHub repo is read
// from raw.githubusercontent.com; an Ether plugin from a repo keeps its
// sidecar tunnel.
func TestGitHubStaticAddonUsesRawFiles(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"https://github.com/yowmamasita/usa-tv-next", "https://raw.githubusercontent.com/yowmamasita/usa-tv-next/HEAD"},
		{"https://github.com/o/r.git", "https://raw.githubusercontent.com/o/r/HEAD"},
		{"https://github.com/o/r/tree/dev", "https://raw.githubusercontent.com/o/r/dev"},
		{"https://github.com/o/r/blob/main/manifest.json", "https://raw.githubusercontent.com/o/r/main"},
	} {
		if got, ok := githubRawBase(c.in); !ok || got != c.want {
			t.Errorf("githubRawBase(%q) = %q, %v; want %q", c.in, got, ok, c.want)
		}
	}
	for _, bad := range []string{"https://github.com/o", "https://example.com/o/r", "https://github.com/o/.."} {
		if got, ok := githubRawBase(bad); ok {
			t.Errorf("githubRawBase(%q) = %q, want no base", bad, got)
		}
	}

	tunnel := NewTunnelServer(TunnelOptions{})
	defer tunnel.Close()
	open := (&Factory{Tunnel: tunnel}).RuntimeFactory()
	addon, err := open(plugins.Installed{ID: "addon.x", SourceURL: "https://github.com/o/r"})
	if err != nil {
		t.Fatal(err)
	}
	defer addon.Close()
	if _, ok := addon.(*contractRuntime).inner.(*URLClient); !ok {
		t.Fatalf("static add-on from GitHub opened %T, want a URL client", addon.(*contractRuntime).inner)
	}
	plugin, err := open(plugins.Installed{ID: "org.example.p", SourceURL: "https://github.com/o/r", Manifest: plugins.Manifest{Ether: &plugins.EtherBlock{}}})
	if err != nil {
		t.Fatal(err)
	}
	defer plugin.Close()
	if _, ok := plugin.(*contractRuntime).inner.(*URLClient); ok {
		t.Fatal("an Ether plugin from GitHub must keep its sidecar tunnel")
	}
}
