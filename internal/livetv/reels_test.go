package livetv

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
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

// Shipped showcase reels store updated_at=0 in SQLite/PostgreSQL so pods air on
// every calendar date. Neither a zero Anchor nor a Unix-epoch-0 Anchor may drag
// weeklySchedule back to 1969-12-11 (or walk 57 years of calendar days).
func TestShippedReelEpochZeroDoesNotWalkFrom1970(t *testing.T) {
	pattern, ok := EraBreakPattern("1990s")
	if !ok {
		t.Fatal("missing 1990s break pattern")
	}
	filler := make([]Item, 120)
	for i := range filler {
		filler[i] = Item{Key: core.RatingKey(1000 + i), Title: "Short", DurationMS: 3 * 60 * 1000}
	}
	for _, anchor := range []time.Time{{}, time.Unix(0, 0).UTC()} {
		c := Channel{
			ID:         "showcase-90s",
			Kind:       "virtual",
			Timezone:   "America/Toronto",
			Seed:       90,
			RepeatDays: 21,
			Filler:     filler,
			Blocks: []Block{{
				ID:         "evening",
				Name:       "Prime",
				Start:      1080,
				End:        1440,
				Mode:       "order",
				Align:      "half",
				Fit:        "pad",
				RepeatDays: 21,
				Items:      []Item{{Key: 1, Title: "Episode", DurationMS: 22 * 60 * 1000, BreakPointsMS: []int64{7 * 60 * 1000, 15 * 60 * 1000}}},
			}},
			Look: &ChannelLook{
				LookID:       "90s",
				Breaks:       "inside",
				BreakContent: "reel",
				ReelID:       "shipped-90s",
				BreakPattern: &pattern,
			},
			clipDuration: testGraphics{}.Duration,
			reel: &ReelSnapshot{
				ID:     "shipped-90s",
				Anchor: anchor,
				Order:  "shuffle",
				Repeat: 4 * time.Hour,
				Clips: []ReelClip{
					{ID: "spot-1", Name: "Spot 1", Duration: 30 * time.Second},
					{ID: "spot-2", Name: "Spot 2", Duration: 30 * time.Second},
					{ID: "spot-3", Name: "Spot 3", Duration: 30 * time.Second},
					{ID: "spot-4", Name: "Spot 4", Duration: 30 * time.Second},
					{ID: "spot-5", Name: "Spot 5", Duration: 30 * time.Second},
				},
			},
		}
		now := time.Date(2026, 11, 9, 20, 0, 0, 0, time.UTC)
		started := time.Now()
		got := Schedule(c, now.Add(-6*time.Hour), now.Add(48*time.Hour))
		elapsed := time.Since(started)
		if len(got) == 0 {
			t.Fatalf("anchor=%v produced empty schedule", anchor)
		}
		if elapsed > 2*time.Second {
			t.Fatalf("anchor=%v walked historical calendar: elapsed=%s", anchor, elapsed)
		}
		pods := 0
		for _, p := range got {
			if p.Item.ReelClipID != "" {
				pods++
			}
		}
		if pods == 0 {
			t.Fatalf("anchor=%v omitted shipped reel pods", anchor)
		}
	}
}
