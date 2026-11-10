package studio

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/plugins"
)

func TestContentKindTemplateDeclaresKindAndLiveSearchToggle(t *testing.T) {
	tpl, ok := LookupTemplate("content_kind")
	if !ok {
		t.Fatal("content_kind template missing")
	}
	if tpl.Kind != plugins.KindContent {
		t.Fatalf("kind = %q, want content", tpl.Kind)
	}
	man, err := plugins.ParseManifest([]byte(tpl.Files["manifest.json"]))
	if err != nil {
		t.Fatalf("template manifest does not parse: %v", err)
	}
	kinds := man.EffectiveEther().Contributes.MediaKinds
	if len(kinds) != 1 || !kinds[0].Browse.LiveSearch {
		t.Fatalf("template should declare one live-search kind: %+v", kinds)
	}
	if err := man.Validate(); err != nil {
		t.Fatalf("template manifest does not validate: %v", err)
	}

	off := false
	_, _, files := applyProjectOptions(tpl, CreateProjectOptions{LiveSearch: &off})
	man2, err := plugins.ParseManifest([]byte(files["manifest.json"]))
	if err != nil {
		t.Fatal(err)
	}
	if got := man2.EffectiveEther().Contributes.MediaKinds[0].Browse.LiveSearch; got {
		t.Fatal("LiveSearch option false did not turn live search off")
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(files["manifest.json"]), &raw); err != nil {
		t.Fatal(err)
	}
}

func TestKindProjectionsExplainFlattening(t *testing.T) {
	decl := plugins.MediaKindMeta{
		Key: "deep",
		Levels: []plugins.KindLevel{
			{Key: "series"}, {Key: "volume"}, {Key: "book"}, {Key: "chapter", Playable: true},
		},
	}
	decl.Present.Plex = &struct {
		Base string `json:"base"`
	}{Base: "show"}
	got := kindProjections(decl, plugins.Manifest{})
	if len(got) != 1 || !strings.Contains(got[0].Summary, "flattened") || !strings.Contains(got[0].Summary, "chapter") {
		t.Fatalf("projections = %+v, want the Plex flattening explained", got)
	}
}
