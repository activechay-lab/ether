package scene_test

import (
	"github.com/braedonsaunders/ether/internal/onair/scene"
	"strings"
	"testing"
)

func TestLimits(t *testing.T) {
	base := `{"v":1,"moment":"ident","duration":8,"poster":3,"layers":[{"id":"a","type":"rect"}]}`
	for _, s := range []string{strings.Replace(base, `"v":1`, `"v":1,"unknown":true`, 1), strings.Replace(base, `"duration":8`, `"duration":6`, 1), strings.Replace(base, `"type":"rect"`, `"type":"rect","blur":65`, 1), strings.Replace(base, `"type":"rect"`, `"type":"particles","count":2001`, 1), strings.Replace(base, `"type":"rect"`, `"type":"rect","repeat":201`, 1), strings.Replace(base, `"type":"rect"`, `"type":"rect","x":{"keys":[[0,0],[1,1,"wrong"]]}`, 1)} {
		if _, e := scene.Decode([]byte(s)); e == nil {
			t.Fatalf("accepted %s", s)
		}
	}
	if _, e := scene.Decode([]byte(base)); e != nil {
		t.Fatal(e)
	}
}
func TestBinding(t *testing.T) {
	b := scene.Bindings{Tokens: map[string]any{"accent": "#ff0000", "type": map[string]any{"mono": "inter"}}, Slots: map[string]string{"next.title": "Hello"}}
	if got := b.Resolve("{accent}@0.5 {type.mono} {next.title}"); got != "#ff0000@0.5 inter Hello" {
		t.Fatal(got)
	}
}
