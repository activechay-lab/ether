package livetv

import (
	"context"
	"github.com/braedonsaunders/ether/internal/config"
	"testing"
)

func TestStyleInheritance(t *testing.T) {
	s := &Service{o: Options{StyleScreens: func(id string) *config.LiveTVScreens {
		return &config.LiveTVScreens{OffAir: "testcard", NoSignal: "static"}
	}}}
	c := Channel{Kind: "virtual"}
	effective := s.graphicsChannel(c)
	if effective.Look != nil || c.Look != nil {
		t.Fatal("unassigned channel must retain built-in look")
	}
	if s.screenStyle(c, true) != "card" || s.screenStyle(c, false) != "error" {
		t.Fatal("unassigned channel must use built-in screens")
	}
	c.Look = &ChannelLook{LookID: "override"}
	if s.graphicsChannel(c).Look.LookID != "override" || s.screenStyle(c, true) != "testcard" {
		t.Fatal("channel style must win")
	}
	c.Screens = &ChannelScreens{OffAir: "static"}
	if s.screenStyle(c, true) != "static" {
		t.Fatal("channel screen must win")
	}
}

func TestStyleUsageFollowsChannelAssignment(t *testing.T) {
	s := testService(t, Options{})
	ctx := context.Background()
	c, err := s.SaveChannel(ctx, Channel{Name: "Style usage", Number: "1", Kind: "virtual", Enabled: true, Items: []Item{{Key: 1, Title: "Fixture", DurationMS: 60000}}})
	if err != nil {
		t.Fatal(err)
	}
	usage, err := s.StyleChannelIDs(ctx)
	if err != nil || len(usage) != 0 {
		t.Fatal(usage, err)
	}
	c.Look = &ChannelLook{LookID: "custom"}
	if _, err = s.SaveChannel(ctx, c); err != nil {
		t.Fatal(err)
	}
	usage, err = s.StyleChannelIDs(ctx)
	if err != nil || len(usage) != 1 || len(usage["custom"]) != 1 {
		t.Fatal(usage, err)
	}
}
