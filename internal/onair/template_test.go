package onair

import (
	"context"
	"reflect"
	"testing"
)

func TestDescriptionTemplates(t *testing.T) {
	s := testService(t, Options{})
	for _, v := range []struct{ prompt, family string }{{"a cosy late-night movie channel", "lateNight"}, {"NEON 84 retro", "retro"}, {"a playful kids channel", "kids"}, {"gold cinema", "cinema"}} {
		base := Look{ID: "draft", ChannelIDs: []string{"one"}}
		a, e := s.Template(context.Background(), v.prompt, base)
		if e != nil {
			t.Fatal(e)
		}
		b, e := s.Template(context.Background(), v.prompt, base)
		if e != nil || !reflect.DeepEqual(a, b) {
			t.Fatal("template must be deterministic", e)
		}
		if a.Family != v.family || a.ID != base.ID || len(a.Moments) < 7 {
			t.Fatalf("incomplete template: %+v", a)
		}
	}
}
