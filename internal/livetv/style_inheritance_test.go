package livetv

import (
	"context"
	"github.com/braedonsaunders/ether/internal/config"
	"testing"
)

func TestStyleInheritance(t *testing.T) {
	s := &Service{o: Options{DefaultStyle: func() string { return "default" }, Screens: func() config.LiveTVScreens { return config.LiveTVScreens{OffAir: "card", NoSignal: "error"} }, StyleScreens: func(id string) *config.LiveTVScreens {
		if id == "default" {
			return &config.LiveTVScreens{OffAir: "bars", NoSignal: "brb", StationID: true}
		}
		return &config.LiveTVScreens{OffAir: "testcard", NoSignal: "static"}
	}}}
	c := Channel{Kind: "virtual"}
	effective := s.graphicsChannel(c)
	if effective.Look == nil || effective.Look.LookID != "default" || c.Look != nil {
		t.Fatal("default must apply without mutating saved channel")
	}
	if s.screenStyle(c, true) != "bars" || s.screenStyle(c, false) != "brb" {
		t.Fatal("style screens must precede server fallback")
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

func TestStyleUsageFollowsChannelAssignmentAndDefault(t *testing.T) {
	s := testService(t, Options{})
	ctx := context.Background()
	c, err := s.SaveChannel(ctx, Channel{Name: "Style usage", Number: "1", Kind: "virtual", Enabled: true, Items: []Item{{Key: 1, Title: "Fixture", DurationMS: 60000}}})
	if err != nil {
		t.Fatal(err)
	}
	usage, err := s.StyleChannelIDs(ctx, "default")
	if err != nil || len(usage["default"]) != 1 {
		t.Fatal(usage, err)
	}
	c.Look = &ChannelLook{LookID: "custom"}
	if _, err = s.SaveChannel(ctx, c); err != nil {
		t.Fatal(err)
	}
	usage, err = s.StyleChannelIDs(ctx, "default")
	if err != nil || len(usage["default"]) != 0 || len(usage["custom"]) != 1 {
		t.Fatal(usage, err)
	}
}
