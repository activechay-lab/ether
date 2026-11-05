package onair

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/onair/scene"
	"testing"
	"time"
)

func TestPluginFeedVersionAndStaleClip(t *testing.T) {
	cache := NewPluginContentCache()
	seg := PluginSegment{Document: scene.Document{V: 1, Moment: "plugin:weather/forecast", Duration: 120}, Version: "v1", Expires: time.Now().Add(time.Hour), Slots: map[string]string{"now.temp": "12"}}
	cache.Publish(map[string]PluginSegment{"plugin:weather/forecast": seg})
	s := &Service{o: Options{PluginSegment: cache.Lookup}, looks: map[string]Look{}}
	req := core.ClipRequest{Moment: "plugin:weather/forecast"}
	one, ok := s.plan(req)
	if !ok {
		t.Fatal("missing plugin plan")
	}
	seg.Version = "v2"
	cache.Publish(map[string]PluginSegment{req.Moment: seg})
	two, ok := s.plan(req)
	if !ok || one.Hash == two.Hash {
		t.Fatal("feed version absent from hash")
	}
	seg.Expires = time.Now().Add(-time.Second)
	cache.Publish(map[string]PluginSegment{req.Moment: seg})
	if _, ok = s.plan(req); ok {
		t.Fatal("stale feed rendered")
	}
	if d, ok := s.Duration("", req.Moment); !ok || d != 120*time.Second {
		t.Fatal("expiry changed duration")
	}
}
