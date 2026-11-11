package availability

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestDigitalPagingFilterAndWindowBounds(t *testing.T) {
	x := setup(t)
	ctx := context.Background()
	c, err := catalog.New(x.s.db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return x.now }))
	if err != nil {
		t.Fatal(err)
	}
	x.s.d.Catalog = c
	x.c.Availability.Mode = config.ModeDigital
	x.c.Availability.ApplyMode()
	for i := int64(1); i <= 30; i++ {
		title := core.Title{Key: core.MovieKey(i), Type: core.TypeMovie, Title: fmt.Sprintf("%03d", i), Released: x.now.Add(-24 * time.Hour)}
		if err := c.UpsertTitle(ctx, title, true); err != nil {
			t.Fatal(err)
		}
		if i > 1 {
			if _, err := x.s.db.Exec(`INSERT INTO catalog_home_release(key,checked,earliest_ts) VALUES(?,1,?)`, title.Key, x.now.Add(-time.Hour).Unix()); err != nil {
				t.Fatal(err)
			}
		}
	}
	pf, err := x.s.PagingFilter(ctx, core.Filter{Type: core.TypeMovie})
	if err != nil {
		t.Fatal(err)
	}
	if !pf.DigitalDiscovery || !pf.DigitalDateEvidence || pf.MovieGateCutoff != 0 || pf.ShowGateCutoff != 0 || pf.EpisodeGateCutoff != 0 {
		t.Fatalf("unexpected digital PagingFilter: %+v", pf)
	}
	page, err := c.Browse(ctx, core.BrowseQuery{Filter: pf, Sort: core.SortTitle, Limit: 10})
	if err != nil || page.Total != 29 || page.Items[0].Key != core.MovieKey(2) {
		t.Fatalf("digital browse: %+v %v", page, err)
	}
}

func TestDigitalWindowUsesHomeDate(t *testing.T) {
	now := time.Now()
	cfg := config.Availability{Mode: config.ModeDigital, MovieEvidenceDays: 90}
	title := core.Title{Key: core.MovieKey(1), Type: core.TypeMovie, Released: now.AddDate(-5, 0, 0)}
	for _, tc := range []struct {
		date time.Time
		want bool
	}{{time.Time{}, true}, {now.Add(time.Hour), true}, {now.Add(-89 * 24 * time.Hour), true}, {now.Add(-90 * 24 * time.Hour), false}} {
		title.HomeReleased = tc.date
		if got := evidenceWindow(title, now, cfg); got != tc.want {
			t.Fatalf("home date %v: %v", tc.date, got)
		}
	}
}

func TestPagingFilterPopulatesStickyGateFields(t *testing.T) {
	x := setup(t)
	ctx := t.Context()
	mf, err := x.s.PagingFilter(ctx, core.Filter{Type: core.TypeMovie})
	if err != nil {
		t.Fatal(err)
	}
	if mf.GatePolicy == "" || mf.MovieGateCutoff == 0 || mf.ShowGateCutoff == 0 || mf.EpisodeGateCutoff == 0 {
		t.Fatalf("movie PagingFilter: %+v", mf)
	}
	sf, err := x.s.PagingFilter(ctx, core.Filter{Type: core.TypeShow})
	if err != nil {
		t.Fatal(err)
	}
	x.c.Availability.MovieMode = config.ModeImmediate
	x.c.Availability.ShowMode = config.ModePlayable
	sf, err = x.s.PagingFilter(ctx, core.Filter{Type: core.TypeShow})
	if err != nil {
		t.Fatal(err)
	}
	if sf.GatePolicy == "" || sf.ShowGateCutoff == 0 || sf.EpisodeGateCutoff == 0 || sf.MovieGateCutoff != 0 {
		t.Fatalf("show PagingFilter: %+v", sf)
	}
	x.c.Availability.MovieMode = config.ModePlayable
	x.c.Availability.ShowMode = config.ModeImmediate
	mixed, err := x.s.PagingFilter(ctx, core.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if mixed.MovieGateCutoff == 0 || mixed.ShowGateCutoff != 0 || mixed.EpisodeGateCutoff != 0 {
		t.Fatalf("per-type mixed PagingFilter: %+v", mixed)
	}
}
