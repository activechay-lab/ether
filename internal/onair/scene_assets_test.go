package onair

import (
	"github.com/braedonsaunders/ether/internal/onair/scene"
	"strings"
	"testing"
)

func TestSceneReferencesExcludeFeedMetadata(t *testing.T) {
	digest := strings.Repeat("a", 64)
	version := strings.Repeat("b", 64)
	bindings := scene.Bindings{Slots: map[string]string{"icon": digest, "feed.version": version, "now.summary": strings.Repeat("x", 64)}}
	layers := []scene.Layer{{Type: "image", Src: "{icon}"}, {Type: "text", Text: "{feed.version}"}, {Type: "text", Text: "{now.summary}"}}
	refs := map[string]string{}
	collectSceneAssets(layers, bindings, refs)
	if len(refs) != 1 || refs[digest] != digest {
		t.Fatalf("asset references: %v", refs)
	}
	if !staticPluginScene(scene.Document{Layers: layers}) {
		t.Fatal("static card not reusable")
	}
	layers[0].Opacity = map[string]any{"keys": []any{[]any{0.0, 0.0}, []any{1.0, 1.0}}}
	if staticPluginScene(scene.Document{Layers: layers}) {
		t.Fatal("animated card frozen")
	}
}
