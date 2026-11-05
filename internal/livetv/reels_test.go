package livetv

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReelScheduleWindowRepeatAndWholeClips(t *testing.T) {
	anchor := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	for _, placement := range []string{"gap", "insert"} {
		for _, order := range []string{"sequence", "shuffle", "sequence-clock", "shuffle-clock"} {
			t.Run(placement+"/"+order, func(t *testing.T) {
				c := Channel{ID: "reel", Kind: "virtual", Mode: "order", Epoch: anchor, Seed: 35, Items: []Item{{Key: 1, Title: "Programme", DurationMS: 2 * 60 * 1000}}, Look: &ChannelLook{LookID: "look", Breaks: "between", BreakContent: "reel", ReelID: "ads", BreakSeconds: 24, Placement: placement}}
				if placement == "gap" {
					c.Align = "half"
				}
				c.clipDuration = testGraphics{}.Duration
				c.reel = &ReelSnapshot{ID: "ads", Anchor: anchor, Order: strings.TrimSuffix(order, "-clock"), Repeat: 5 * time.Minute, Clips: []ReelClip{{ID: "a", Name: "A", Duration: 12 * time.Second}, {ID: "b", Name: "B", Duration: 20 * time.Second}, {ID: "c", Name: "C", Duration: 28 * time.Second}}}
				if strings.HasSuffix(order, "-clock") {
					c.Clock = []ClockSlot{{SegmentID: "plugin:test/clock", Minute: 5, EveryMinutes: 60, From: 0, Until: 1440}}
				}
				from, until := anchor.Add(3*time.Hour), anchor.Add(48*time.Hour)
				all := Schedule(c, from, until)
				start, end := anchor.Add(23*time.Hour+5*time.Second), anchor.Add(40*time.Hour)
				narrow := Schedule(c, start, end)
				var overlap []Programme
				for _, p := range all {
					if p.End.After(start) && p.Start.Before(end) {
						overlap = append(overlap, p)
					}
				}
				if !reflect.DeepEqual(overlap, narrow) {
					t.Fatal("reel schedule depends on query window")
				}
				last := map[string]time.Time{}
				count := 0
				for _, p := range all {
					if p.Item.ReelClipID == "" {
						continue
					}
					count++
					var duration time.Duration
					for _, clip := range c.reel.Clips {
						if clip.ID == p.Item.ReelClipID {
							duration = clip.Duration
						}
					}
					if p.End.Sub(p.Start) != duration {
						t.Fatalf("clip cut: %+v", p)
					}
					if previous, ok := last[p.Item.ReelClipID]; ok && p.Start.Sub(previous) < c.reel.Repeat {
						t.Fatalf("repeat window violated: %s, %s", previous, p.Start)
					}
					last[p.Item.ReelClipID] = p.Start
				}
				if count == 0 {
					t.Fatal("no reel clips aired")
				}
			})
		}
	}
}
