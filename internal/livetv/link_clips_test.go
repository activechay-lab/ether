package livetv

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

type linkTestGraphics struct {
	mu      sync.Mutex
	ensured []core.ClipRequest
	cached  map[string]string
	aired   map[string]int
}

func (g *linkTestGraphics) Clip(_ context.Context, _ core.ClipRequest) (core.ClipRef, bool) {
	return core.ClipRef{}, false
}

func (g *linkTestGraphics) Ensure(_ context.Context, r core.ClipRequest) {
	g.mu.Lock()
	g.ensured = append(g.ensured, r)
	g.mu.Unlock()
}

func (g *linkTestGraphics) Duration(_, moment string) (time.Duration, bool) {
	switch moment {
	case "ident", "break", "back":
		return 4 * time.Second, true
	case "upnext", "tonight":
		return 8 * time.Second, true
	case "offair":
		return 10 * time.Second, true
	}
	return 0, false
}

func (g *linkTestGraphics) PeekCachedReelClip(reelID, clipID string) (string, time.Duration, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p := g.cached[reelID+"/"+clipID]
	if p == "" {
		return "", 0, false
	}
	return p, 30 * time.Second, true
}

func (g *linkTestGraphics) CachedReelClip(reelID, clipID string) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p := g.cached[reelID+"/"+clipID]
	if p == "" {
		return "", false
	}
	if g.aired == nil {
		g.aired = map[string]int{}
	}
	g.aired[reelID+"/"+clipID]++
	return p, true
}

func (g *linkTestGraphics) reelEnsureRequests() []core.ClipRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []core.ClipRequest
	for _, r := range g.ensured {
		if r.ReelClipID != "" {
			out = append(out, r)
		}
	}
	return out
}

type showcaseTestCatalog struct {
	core.Catalog
	shows  []core.Title
	movies []core.Title
}

func (c *showcaseTestCatalog) Browse(_ context.Context, q core.BrowseQuery) (core.Page, error) {
	if q.Filter.Type == core.TypeMovie {
		return core.Page{Items: c.movies, Total: len(c.movies)}, nil
	}
	return core.Page{Items: c.shows, Total: len(c.shows)}, nil
}

func (c *showcaseTestCatalog) Get(_ context.Context, k core.RatingKey) (*core.Title, error) {
	for i := range c.shows {
		if c.shows[i].Key == k {
			return &c.shows[i], nil
		}
	}
	for i := range c.movies {
		if c.movies[i].Key == k {
			return &c.movies[i], nil
		}
	}
	return nil, core.ErrNotFound
}

func (c *showcaseTestCatalog) Children(context.Context, core.RatingKey) ([]core.Title, error) {
	return nil, nil
}

func (c *showcaseTestCatalog) Grandchildren(_ context.Context, showKey core.RatingKey) ([]core.Title, error) {
	var eps []core.Title
	for n := 1; n <= 4; n++ {
		eps = append(eps, core.Title{
			Key:     core.EpisodeKey(int64(showKey), 1, n),
			Type:    core.TypeEpisode,
			Title:   "Episode",
			Season:  1,
			Episode: n,
			Runtime: 22 * time.Minute,
		})
	}
	return eps, nil
}

func (c *showcaseTestCatalog) CollectionItems(context.Context, core.RatingKey) ([]core.Title, error) {
	return nil, nil
}

func TestLinkClipsResolveOnlyForWatchedChannels(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	anchor := now.Add(-24 * time.Hour)
	g := &linkTestGraphics{}
	reels := map[string]ReelSnapshot{
		"reel-watched": {
			ID:     "reel-watched",
			Anchor: anchor,
			Order:  "sequence",
			Clips:  []ReelClip{{ID: "w1", Name: "Watched Spot", Duration: 30 * time.Second, Link: true}},
		},
		"reel-idle": {
			ID:     "reel-idle",
			Anchor: anchor,
			Order:  "sequence",
			Clips:  []ReelClip{{ID: "u1", Name: "Idle Spot", Duration: 30 * time.Second, Link: true}},
		},
	}
	s := testService(t, Options{
		Graphics: g,
		Now:      func() time.Time { return now },
		Reels: func(_ context.Context, id string) (ReelSnapshot, error) {
			r, ok := reels[id]
			if !ok {
				return ReelSnapshot{}, core.ErrNotFound
			}
			return r, nil
		},
	})
	for _, spec := range []struct{ id, num, reel string }{
		{"watched-ch", "101", "reel-watched"},
		{"idle-ch", "102", "reel-idle"},
	} {
		ch := Channel{
			ID: spec.id, Number: spec.num, Name: spec.id, Kind: "virtual", Enabled: true,
			Mode: "order", Timezone: "UTC", Epoch: now,
			Items: []Item{{Key: core.MovieKey(1), Title: "Show", DurationMS: int64(5 * time.Minute / time.Millisecond)}},
			Look: &ChannelLook{
				LookID: "showcase-80", Breaks: "between", BreakContent: "reel",
				ReelID: spec.reel, BreakSeconds: 30, Placement: "insert",
			},
		}
		raw, _ := json.Marshal(ch)
		if _, err := s.o.DB.ExecContext(t.Context(), `INSERT INTO livetv_channels(id,data) VALUES(?,?)`, ch.ID, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	s.refreshReels()

	// Unwatched channels must trigger zero link clip resolutions.
	s.ensureGraphics()
	if got := g.reelEnsureRequests(); len(got) != 0 {
		t.Fatalf("unwatched channels queued %d reel clip resolutions, want 0: %+v", len(got), got)
	}

	// Watching watched-ch resolves only watched-ch's imminent break clips.
	s.mu.Lock()
	s.noteWatchedLocked("watched-ch")
	s.mu.Unlock()
	s.ensureGraphics()
	got := g.reelEnsureRequests()
	if len(got) == 0 {
		t.Fatal("watched channel queued 0 reel clip resolutions")
	}
	for _, r := range got {
		if r.ChannelID != "watched-ch" || r.ReelID != "reel-watched" {
			t.Fatalf("unexpected channel/reel resolution queued: %+v", r)
		}
	}
}

func TestUnresolvableLinkClipSkipsSilentlyWithoutShiftingGuide(t *testing.T) {
	epoch := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	from := epoch.Add(12 * time.Hour)
	until := from.Add(10 * time.Minute)

	findFirstReelSpot := func(t *testing.T, c Channel) Programme {
		t.Helper()
		for _, p := range Schedule(c, from, until) {
			if p.Item.ReelClipID == "spot-a" {
				return p
			}
		}
		t.Fatalf("no spot-a in schedule: %+v", Schedule(c, from, until))
		return Programme{}
	}

	t.Run("next_cached_clip", func(t *testing.T) {
		now := from
		g := &linkTestGraphics{
			cached: map[string]string{
				"ads/spot-b": "/tmp/cached-spot-b.mp4",
			},
		}
		s := testService(t, Options{Graphics: g, Now: func() time.Time { return now }})
		s.reelSnapshots = map[string]*ReelSnapshot{
			"ads": {
				ID:     "ads",
				Anchor: epoch,
				Order:  "sequence",
				Clips: []ReelClip{
					{ID: "spot-a", Name: "Uncached Spot A", Duration: 30 * time.Second, Link: true},
					{ID: "spot-b", Name: "Cached Spot B", Duration: 30 * time.Second, Path: "/tmp/cached-spot-b.mp4", Link: true},
				},
			},
		}
		c := Channel{
			ID: "ch", Number: "80", Name: "CH", Kind: "virtual", Enabled: true,
			Mode: "order", Timezone: "UTC", Epoch: epoch,
			Items: []Item{{Key: core.MovieKey(1), Title: "Feature", DurationMS: int64(2 * time.Minute / time.Millisecond)}},
			Look:  &ChannelLook{LookID: "showcase-80", Breaks: "between", BreakContent: "reel", ReelID: "ads", BreakSeconds: 30, Placement: "insert"},
		}
		c = s.graphicsChannel(c)
		wantSpot := findFirstReelSpot(t, c)
		now = wantSpot.Start
		rawSched := Schedule(c, from, until)
		guideBefore := s.broadcastGuide(c, from, until)
		aired := s.broadcastSchedule(t.Context(), c, wantSpot.Start, until, true)

		var sub *Programme
		for i := range aired {
			if aired[i].Start.Equal(wantSpot.Start) {
				sub = &aired[i]
				break
			}
		}
		if sub == nil || sub.Item.ReelClipID != "spot-b" || !sub.End.Equal(wantSpot.End) {
			t.Fatalf("expected uncached spot-a to substitute cached spot-b across exact slot, got %+v", sub)
		}
		if path := s.openReelClipPath(c, "ads", "spot-b"); path != "/tmp/cached-spot-b.mp4" || g.aired["ads/spot-b"] != 1 {
			t.Fatalf("openReelClipPath=%q aired=%d", path, g.aired["ads/spot-b"])
		}
		var rawNext, airedNext Programme
		for _, p := range rawSched {
			if p.Item.Key != 0 && p.Start.After(wantSpot.Start) {
				rawNext = p
				break
			}
		}
		for _, p := range aired {
			if p.Item.Key != 0 && p.Start.After(wantSpot.Start) {
				airedNext = p
				break
			}
		}
		if rawNext.ID != airedNext.ID || !rawNext.Start.Equal(airedNext.Start) || !rawNext.End.Equal(airedNext.End) {
			t.Fatalf("next programme shifted: got %+v, want %+v", airedNext, rawNext)
		}
		if !reflect.DeepEqual(guideBefore, s.broadcastGuide(c, from, until)) {
			t.Fatal("substitution altered broadcastGuide")
		}
	})

	t.Run("channel_filler", func(t *testing.T) {
		now := from
		g := &linkTestGraphics{}
		s := testService(t, Options{Graphics: g, Playback: &fakePlayback{}, Now: func() time.Time { return now }})
		s.reelSnapshots = map[string]*ReelSnapshot{
			"ads": {
				ID:     "ads",
				Anchor: epoch,
				Order:  "sequence",
				Clips:  []ReelClip{{ID: "spot-a", Name: "Dead Link", Duration: 30 * time.Second, Link: true}},
			},
		}
		c := Channel{
			ID: "ch-fill", Number: "81", Name: "CH Fill", Kind: "virtual", Enabled: true,
			Mode: "order", Timezone: "UTC", Epoch: epoch,
			Items:  []Item{{Key: core.MovieKey(1), Title: "Feature", DurationMS: int64(2 * time.Minute / time.Millisecond)}},
			Filler: []Item{{Key: core.MovieKey(99), Title: "Short Cartoon", DurationMS: 60000}},
			Look:   &ChannelLook{LookID: "showcase-80", Breaks: "between", BreakContent: "reel", ReelID: "ads", BreakSeconds: 30, Placement: "insert"},
		}
		c = s.graphicsChannel(c)
		wantSpot := findFirstReelSpot(t, c)
		now = wantSpot.Start
		aired := s.broadcastSchedule(t.Context(), c, wantSpot.Start, until, true)
		var sub *Programme
		for i := range aired {
			if aired[i].Start.Equal(wantSpot.Start) {
				sub = &aired[i]
				break
			}
		}
		if sub == nil || sub.Item.Key != core.MovieKey(99) || sub.Item.Slate != "" || !sub.End.Equal(wantSpot.End) {
			t.Fatalf("expected channel filler over [Start,End], got %+v", sub)
		}
	})

	t.Run("live_break_fallback_never_offair", func(t *testing.T) {
		now := from
		g := &linkTestGraphics{}
		s := testService(t, Options{Graphics: g, Now: func() time.Time { return now }})
		s.reelSnapshots = map[string]*ReelSnapshot{
			"ads": {
				ID:     "ads",
				Anchor: epoch,
				Order:  "sequence",
				Clips:  []ReelClip{{ID: "spot-a", Name: "Dead Link", Duration: 30 * time.Second, Link: true}},
			},
		}
		c := Channel{
			ID: "ch-fb", Number: "82", Name: "CH Fallback", Kind: "virtual", Enabled: true,
			Mode: "order", Timezone: "UTC", Epoch: epoch,
			Items: []Item{{Key: core.MovieKey(1), Title: "Feature", DurationMS: int64(2 * time.Minute / time.Millisecond)}},
			Look:  &ChannelLook{LookID: "showcase-80", Breaks: "between", BreakContent: "reel", ReelID: "ads", BreakSeconds: 30, Placement: "insert"},
		}
		c = s.graphicsChannel(c)
		wantSpot := findFirstReelSpot(t, c)
		now = wantSpot.Start
		aired := s.broadcastSchedule(t.Context(), c, wantSpot.Start, until, true)
		var sub *Programme
		for i := range aired {
			if aired[i].Start.Equal(wantSpot.Start) {
				sub = &aired[i]
				break
			}
		}
		if sub == nil || !sub.graphicsFallback || !interstitial(sub.Item) || sub.Item.Slate == offAirSlate {
			t.Fatalf("expected live moment fallback (never off-air), got %+v", sub)
		}
	})
}

func TestExisting036ShowcaseChannelsUpgradeWithShippedReels(t *testing.T) {
	ctx := t.Context()
	db := store.OpenTest(t)
	seedDay := time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC)
	now := time.Date(2026, 11, 9, 15, 0, 0, 0, time.UTC)

	cat := &showcaseTestCatalog{
		shows: []core.Title{
			{Key: core.ShowKey(10), Type: core.TypeShow, Title: "Era Show", Year: 1985, Runtime: 24 * time.Minute},
		},
		movies: []core.Title{
			{Key: core.MovieKey(20), Type: core.TypeMovie, Title: "Era Movie", Year: 1986, Runtime: 95 * time.Minute},
		},
	}
	s := testService(t, Options{
		DB:      db,
		Catalog: cat,
		Now:     func() time.Time { return now },
	})

	// Simulate existing 0.3.6 install (version 1: no ReelID, no BreakPattern, refreshed 1h ago).
	for _, r := range showcaseRecipes() {
		v1 := r
		v1.version = 1
		v1.reelID = ""
		v1.decade = ""
		id := "showcase-" + r.id
		c := v1.channel(id, r.number, "UTC", seedDay)
		origHash := showcaseRecipeHash(c)
		c.Enabled = true

		switch r.id {
		case "orbit-00":
			// Owner customized ORBIT 00 on 0.3.6 -> upgrade must leave it alone!
			c.Look.UpNext = false
			c.Name = "MY ORBIT 2000"
		case "shift-10":
			// Owner deleted SHIFT 10 on 0.3.6 -> upgrade must not recreate it!
			if _, err := db.ExecContext(ctx, `INSERT INTO livetv_showcase(id,channel_id,version,recipe_hash,state,refreshed_ms,attempted_ms) VALUES(?,?,?,?,?,?,?)`,
				r.id, id, 1, origHash, showcaseLive, now.Add(-time.Hour).UnixMilli(), now.Add(-time.Hour).UnixMilli()); err != nil {
				t.Fatal(err)
			}
			continue
		}

		raw, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO livetv_showcase(id,channel_id,version,recipe_hash,state,refreshed_ms,attempted_ms) VALUES(?,?,?,?,?,?,?)`,
			r.id, id, 1, origHash, showcaseLive, now.Add(-time.Hour).UnixMilli(), now.Add(-time.Hour).UnixMilli()); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO livetv_channels(id,data) VALUES(?,?)`, id, string(raw)); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.seedShowcase(ctx); err != nil {
		t.Fatalf("seedShowcase: %v", err)
	}
	s.refreshShowcase(ctx)

	loadChan := func(id string) (Channel, error) {
		var raw string
		if err := db.QueryRowContext(ctx, `SELECT data FROM livetv_channels WHERE id=?`, id).Scan(&raw); err != nil {
			return Channel{}, err
		}
		var c Channel
		return c, json.Unmarshal([]byte(raw), &c)
	}

	// 1. Untouched CHROMA 80 and RIOT 90 upgraded immediately with their decade reels & break patterns.
	for _, want := range []struct{ chanID, reelID, preset string }{
		{"showcase-chroma-80", "showcase-reel-80", "1980s"},
		{"showcase-riot-90", "showcase-reel-90", "1990s"},
	} {
		c, err := loadChan(want.chanID)
		if err != nil {
			t.Fatalf("load %s: %v", want.chanID, err)
		}
		if !c.Enabled || c.Epoch != dayStart(seedDay, time.UTC).UTC() {
			t.Fatalf("%s lost Enabled or Epoch on upgrade: enabled=%v epoch=%v", want.chanID, c.Enabled, c.Epoch)
		}
		if c.Look == nil || c.Look.ReelID != want.reelID || c.Look.BreakContent != "reel" || c.Look.BreakPattern == nil || c.Look.BreakPattern.Preset != want.preset || c.Look.BreakPattern.Inside {
			t.Fatalf("%s did not attach shipped decade reel look: %+v", want.chanID, c.Look)
		}
	}

	// 2. Owner-edited ORBIT 00 kept owner edits and was not overwritten.
	orbit, err := loadChan("showcase-orbit-00")
	if err != nil {
		t.Fatalf("load orbit: %v", err)
	}
	if orbit.Name != "MY ORBIT 2000" || orbit.Look.UpNext || orbit.Look.ReelID != "" {
		t.Fatalf("owner-edited showcase-orbit-00 was overwritten: %+v look=%+v", orbit, orbit.Look)
	}

	// 3. Owner-deleted SHIFT 10 stayed deleted.
	if _, err := loadChan("showcase-shift-10"); err == nil {
		t.Fatal("owner-deleted showcase-shift-10 was resurrected")
	}
}
