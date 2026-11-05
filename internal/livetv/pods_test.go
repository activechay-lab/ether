package livetv

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func podChannel() Channel {
	at := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	p, _ := EraBreakPattern("1990s")
	c := Channel{ID: "era", Kind: "virtual", Mode: "order", Timezone: "UTC", Epoch: at, Seed: 35, Items: []Item{{Key: 1, Title: "Show", DurationMS: 22 * 60 * 1000, BreakPointsMS: []int64{7 * 60 * 1000, 15 * 60 * 1000}}}, Look: &ChannelLook{LookID: "retro", BreakContent: "reel", ReelID: "ads", Placement: "insert", BreakPattern: &p}, clipDuration: testGraphics{}.Duration, reel: &ReelSnapshot{ID: "ads", Anchor: at}}
	for i := 0; i < 50; i++ {
		c.reel.Clips = append(c.reel.Clips, ReelClip{ID: fmt.Sprint(i), Name: "Spot", Duration: 30 * time.Second})
	}
	return c
}
func TestPodLimitsDeterminismAndNoRepeats(t *testing.T) {
	c := podChannel()
	at := c.Epoch
	c.momentHistory = &momentHistory{last: map[string]time.Time{}}
	first := c.podItems(at, 3*time.Minute)
	if len(first) < 4 {
		t.Fatal("missing pod", first)
	}
	clone := podChannel()
	clone.momentHistory = &momentHistory{last: map[string]time.Time{}}
	if !reflect.DeepEqual(first, clone.podItems(at, 3*time.Minute)) {
		t.Fatal("not deterministic")
	}
	seen := map[string]bool{}
	for _, it := range first {
		if it.ReelClipID != "" {
			seen[it.ReelClipID] = true
		}
	}
	second := c.podItems(at.Add(15*time.Minute), 3*time.Minute)
	for _, it := range second {
		if seen[it.ReelClipID] {
			t.Fatal("repeated spot")
		}
	}
	if got := c.podItems(at.Add(16*time.Minute), 3*time.Minute); len(got) != 0 {
		t.Fatal("frequency limit ignored")
	}
	clone.momentHistory = &momentHistory{last: map[string]time.Time{}}
	if got := clone.podItems(at, 30*time.Second); len(got) != 0 || len(clone.momentHistory.last) != 0 {
		t.Fatal("under-minimum pod consumed history")
	}
	for _, pod := range [][]Item{first, second} {
		var duration int64
		spots := 0
		for _, it := range pod {
			duration += it.DurationMS
			if it.ReelClipID != "" {
				spots++
			}
		}
		p := c.Look.BreakPattern
		if duration < int64(p.MinSeconds)*1000 || duration > int64(p.MaxSeconds)*1000 || spots < p.MinSpots || spots > p.MaxSpots {
			t.Fatal("limits violated", duration, spots)
		}
	}
}
func TestPodsGuideAndSourceResume(t *testing.T) {
	c := podChannel()
	from, until := c.Epoch, c.Epoch.Add(2*time.Hour)
	all := Schedule(c, from, until)
	guide := guideSchedule(c, from, until)
	service := &Service{o: Options{Graphics: testGraphics{cached: true}}, reelSnapshots: map[string]*ReelSnapshot{"ads": c.reel}}
	broadcastGuide := service.broadcastGuide(c, from, until)
	if len(guide) != len(broadcastGuide) {
		t.Fatal("broadcast guide row count", len(guide), len(broadcastGuide))
	}
	for i, g := range guide {
		b := broadcastGuide[i]
		if g.ID != b.ID || !g.Start.Equal(b.Start) || !g.End.Equal(b.End) || !reflect.DeepEqual(g.Item, b.Item) {
			t.Fatalf("broadcast guide slot mismatch: %+v / %+v", g, b)
		}
	}
	pods, resumed := 0, 0
	for _, p := range all {
		if p.Item.Slate == "reel" {
			pods++
		}
		if p.sourceOffset > 0 {
			resumed++
			if p.Offset(p.Start) != p.sourceOffset {
				t.Fatal("resume lost")
			}
		}
		if interstitial(p.Item) {
			covered := false
			for _, g := range guide {
				if !p.Start.Before(g.Start) && !p.End.After(g.End) {
					covered = true
				}
			}
			if !covered {
				t.Fatal("pod outside guide slot", p)
			}
		}
	}
	if pods == 0 || resumed == 0 {
		t.Fatal("no inside programme pods", pods, resumed)
	}
	for _, g := range guide {
		if interstitial(g.Item) {
			t.Fatal("pod leaked to guide")
		}
	}
	narrow := Schedule(c, from.Add(20*time.Minute), until)
	var overlap []Programme
	for _, p := range all {
		if p.End.After(from.Add(20 * time.Minute)) {
			overlap = append(overlap, p)
		}
	}
	if !reflect.DeepEqual(overlap, narrow) {
		t.Fatal("query windows differ")
	}
}
