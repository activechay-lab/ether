package livetv

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestChannelReadinessInvariants(t *testing.T) {
	now := time.Unix(1800, 0)
	slot := Programme{ID: "slot", ChannelID: "c", Start: now.Add(-10 * time.Minute), End: now.Add(20 * time.Minute), Item: Item{Key: core.MovieKey(1)}}
	item := Item{Key: core.MovieKey(2), DurationMS: (40 * time.Minute).Milliseconds()}
	sub, ok := joinStandIn(slot, item, now)
	if !ok || sub.Offset(now) != 10*time.Minute || sub.Start != slot.Start || sub.End != slot.End {
		t.Fatalf("lost wall clock: %+v", sub)
	}
	short := item
	short.DurationMS = (8 * time.Minute).Milliseconds()
	sub, ok = joinStandIn(slot, short, now)
	if !ok || sub.Offset(now) != 6*time.Minute {
		t.Fatalf("short replacement offset: %+v", sub)
	}
	checks := 0
	s := testService(t, Options{Now: func() time.Time { return now }, Availability: availFunc(func(k core.RatingKey) (bool, bool) { checks++; return true, k == item.Key })})
	c := Channel{ID: "c", Items: []Item{slot.Item, item}}
	future := slot
	future.Start = now.Add(time.Minute)
	future.End = future.Start.Add(30 * time.Minute)
	s.checkReady(context.Background(), slot.Item.Key)
	s.replaceUnready(c, []Programme{future})
	if checks != 1 {
		t.Fatal("unwatched guide queried a provider")
	}
	s.checkReady(context.Background(), item.Key)
	s.replaceUnready(c, []Programme{future})
	if checks != 2 {
		t.Fatal("known guide replacement queried a provider")
	}
	guide := s.readyProgrammes(c, []Programme{future})
	if guide[0].Item.Key != item.Key || guide[0].Start != future.Start || guide[0].End != future.End {
		t.Fatalf("guide substitution: %+v", guide)
	}
	firstViewer := future
	firstViewer.readinessViewer = 1
	firstViewer.Item = item
	s.latchProgramme(firstViewer)
	secondViewer := future
	secondViewer.readinessViewer = 2
	if got := s.readyProgrammes(c, []Programme{secondViewer}); got[0].Item.Key != future.Item.Key {
		t.Fatal("personal replacement crossed viewer identities")
	}

}
