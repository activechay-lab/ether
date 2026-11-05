package plex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestPlexTimelinePlayLatches(t *testing.T) {
	cases := []struct {
		name  string
		steps []struct {
			key            core.RatingKey
			position       int
			status, answer string
			offset         int
			count          int
		}
	}{}
	// Independent cases start with pristine state and a single client's track.
	type beat = struct {
		key            core.RatingKey
		position       int
		status, answer string
		offset         int
		count          int
	}
	a, b := fightClub.Key, alien.Key
	cases = append(cases,
		struct {
			name  string
			steps []beat
		}{"cold end and zero cannot complete", []beat{{a, 950000, "playing", "ignore", 0, 0}, {a, 0, "playing", "ignore", 0, 0}, {a, 950000, "stopped", "ignore", 0, 0}}},
		struct {
			name  string
			steps []beat
		}{"completion closes on stop", []beat{{a, 3000, "playing", "ignore", 0, 0}, {a, 920000, "playing", "complete", 0, 1}, {a, 930000, "stopped", "complete", 0, 1}, {a, 950000, "playing", "ignore", 0, 1}}},
		struct {
			name  string
			steps []beat
		}{"stop never clears resume", []beat{{a, 70000, "playing", "progress", 70000, 0}, {a, 3000, "stopped", "progress", 70000, 0}, {a, 3000, "paused", "ignore", 70000, 0}}},
		struct {
			name  string
			steps []beat
		}{"progress survives stop", []beat{{a, 70000, "playing", "progress", 70000, 0}, {a, 75000, "stopped", "progress", 75000, 0}, {a, 3000, "playing", "progress", 0, 0}}},
		struct {
			name  string
			steps []beat
		}{"item switch resets resume latch", []beat{{a, 70000, "playing", "progress", 70000, 0}, {b, 3000, "playing", "ignore", 0, 0}, {a, 3000, "playing", "ignore", 70000, 0}}},
		struct {
			name  string
			steps []beat
		}{"crossing reads session previous item", []beat{{a, 3000, "playing", "ignore", 0, 0}, {b, 920000, "playing", "complete", 0, 1}, {a, 930000, "stopped", "ignore", 0, 0}}},
	)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			defer f.srv.Close()
			watch := state.New(store.OpenTest(t), state.Options{})
			f.srv.d.State = watch
			for i, step := range tc.steps {
				rec := f.get(t, fmt.Sprintf("/:/timeline?ratingKey=%s&state=%s&time=%d&duration=1000000", step.key, step.status, step.position), "Accept", "application/json")
				if rec.Code != http.StatusOK {
					t.Fatalf("step %d: %d %s", i, rec.Code, rec.Body.String())
				}
				var got struct {
					MC struct {
						PlaybackState string `json:"playbackState"`
						ViewCount     int    `json:"viewCount"`
						ViewOffset    int    `json:"viewOffset"`
					} `json:"MediaContainer"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				wantOffset := step.offset
				if wantOffset == 0 {
					wantOffset = -1
				}
				if got.MC.PlaybackState != step.answer || got.MC.ViewCount != step.count || got.MC.ViewOffset != wantOffset {
					t.Fatalf("step %d: body %s; want %s count=%d offset=%d", i, rec.Body.String(), step.answer, step.count, wantOffset)
				}
			}
		})
	}
}

func TestPlexTimelineNoopDurationAndOffline(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	at := time.Unix(1000000, 0)
	watch := state.New(store.OpenTest(t), state.Options{Now: func() time.Time { return at }})
	f.srv.d.State = watch
	key := fightClub.Key.String()
	call := func(query string) {
		t.Helper()
		rec := f.get(t, "/:/timeline?ratingKey="+key+"&"+query)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", query, rec.Code, rec.Body.String())
		}
	}
	call("state=playing&time=70000&duration=1000000")
	before, _ := watch.Get(context.Background(), ownerUser.ID, fightClub.Key)
	at = at.Add(time.Hour)
	for _, q := range []string{"state=playing&time=70000&duration=1000000", "state=playing&time=80000", "state=playing&time=80000&duration=0"} {
		call(q)
	}
	after, _ := watch.Get(context.Background(), ownerUser.ID, fightClub.Key)
	if after.ViewOffset != before.ViewOffset || !after.LastViewedAt.Equal(before.LastViewedAt) || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("no-op changed state: %+v -> %+v", before, after)
	}
	rec := f.get(t, "/:/timeline?ratingKey="+key+"&state=playing&time=1000001&duration=1000000")
	if rec.Code != 400 {
		t.Fatalf("impossible time: %d", rec.Code)
	}
	call("state=stopped&offline=1&viewCount=3")
	after, _ = watch.Get(context.Background(), ownerUser.ID, fightClub.Key)
	if after.ViewCount != 1 || after.ViewOffset != 0 {
		t.Fatalf("offline mark: %+v", after)
	}
	call("state=stopped&offline=1&viewCount=0")
	after, _ = watch.Get(context.Background(), ownerUser.ID, fightClub.Key)
	if after.ViewCount != 0 {
		t.Fatalf("offline clear: %+v", after)
	}
}
