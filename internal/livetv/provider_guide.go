package livetv

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/livetv/guideprovider"
	"github.com/braedonsaunders/ether/internal/store"
)

// Persisted source identity; this is an upstream provider, not the requesting frontend.
const providerGuideKindID = "plex"

func init() {
	store.Register("livetv", 5, `CREATE TABLE livetv_plex_guide_gate (id INTEGER PRIMARY KEY, holder TEXT NOT NULL, locked_until BIGINT NOT NULL, next_at BIGINT NOT NULL, status INTEGER NOT NULL DEFAULT 0);`)
}
func (s *Service) GuideLineups(ctx context.Context, country, postal string) ([]guideprovider.Lineup, error) {
	if s.o.GuideProvider == nil {
		return nil, fmt.Errorf("guide provider is unavailable")
	}
	return s.o.GuideProvider.Lineups(ctx, country, postal)
}

// guideGridDates covers the requested UTC window, tomorrow's UTC grid, and
// the server-local date when it differs. Plex grids overlap across dates.
func guideGridDates(now, from, until time.Time) []time.Time {
	dates := map[string]time.Time{}
	add := func(t time.Time) {
		date := t.Format("2006-01-02")
		if _, ok := dates[date]; !ok {
			dates[date], _ = time.Parse("2006-01-02", date)
		}
	}
	utc := now.UTC()
	add(utc)
	add(utc.AddDate(0, 0, 1))
	add(now)
	first := from.UTC()
	first = time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, time.UTC)
	for d := first; d.Before(until.UTC()); d = d.AddDate(0, 0, 1) {
		add(d)
	}
	out := make([]time.Time, 0, len(dates))
	for _, d := range dates {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// Later entries replace earlier ones for the same channel and start. Sort
// before presentation as upstream groups airings by metadata, not by time.
func uniqueGuideProgrammes(ps []Programme) []Programme {
	type key struct {
		channel string
		start   int64
	}
	seen := make(map[key]Programme, len(ps))
	for _, p := range ps {
		seen[key{p.ChannelID, p.Start.UnixMilli()}] = p
	}
	out := make([]Programme, 0, len(seen))
	for _, p := range seen {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Start.Equal(out[j].Start) {
			return out[i].ChannelID < out[j].ChannelID
		}
		return out[i].Start.Before(out[j].Start)
	})
	return out
}

func (s *Service) guideGrids(ctx context.Context, key string, dates []time.Time, budget *int) ([]Programme, error) {
	if s.o.GuideProvider == nil {
		return nil, fmt.Errorf("guide provider is unavailable")
	}
	var programmes []Programme
	for _, date := range dates {
		raw, err := s.o.GuideProvider.Grid(ctx, key, date, budget)
		var ps []Programme
		for _, p := range raw {
			ps = append(ps, Programme{ChannelID: p.ChannelID, Start: p.Start, End: p.End, Item: p.Item})
		}
		if err != nil {
			return nil, err
		}
		programmes = append(programmes, ps...)
	}
	return uniqueGuideProgrammes(programmes), nil
}

type providerGuideKind struct{}

func (providerGuideKind) ID() string              { return providerGuideKindID }
func (providerGuideKind) Matches(src Source) bool { return src.Kind == providerGuideKindID }
func (providerGuideKind) Describe() SourceDescribe {
	return SourceDescribe{Label: extend.T("Plex guide"), Icon: "calendar-range", Editor: "guide", YieldsGuide: true, Order: 31}
}
func (providerGuideKind) Fetch(ctx context.Context, s *Service, src *Source) (Fetched, error) {
	if s.o.GuideProvider == nil {
		return Fetched{}, fmt.Errorf("guide provider is unavailable")
	}
	budget := 80
	channels, err := s.o.GuideProvider.Channels(ctx, src.LineupID, &budget)
	if err != nil {
		return Fetched{}, err
	}
	g := XMLGuide{Channels: map[string]Channel{}, Names: map[string][]string{}}
	for _, c := range channels {
		g.Channels[c.ID] = Channel{Name: c.Name, Logo: c.Logo}
		g.Names[c.ID] = c.Aliases
	}
	cs, err := s.Channels(ctx)
	if err != nil {
		return Fetched{}, err
	}
	used := map[string]bool{}
	for _, c := range cs {
		if c.Kind == "iptv" && guideSource(c) == src.ID && c.GuideID != "" {
			if _, ok := g.Channels[c.GuideID]; ok {
				used[c.GuideID] = true
			}
		}
	}
	keys := make([]string, 0, len(used))
	for k := range used {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// Retain existing listings while rotating through bounded batches; a large
	// lineup must not be starved by the budget or erased on each refresh.
	rows, err := s.o.DB.QueryContext(ctx, `SELECT data FROM livetv_programmes WHERE source_id=? AND end_ms>?`, src.ID, s.o.Now().UnixMilli())
	if err != nil {
		return Fetched{}, err
	}
	for rows.Next() {
		var b []byte
		var p Programme
		if err = rows.Scan(&b); err != nil {
			break
		}
		if json.Unmarshal(b, &p) == nil && used[p.ChannelID] {
			g.Programmes = append(g.Programmes, p)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return Fetched{}, err
	}
	if len(keys) > 0 {
		start := src.PlexCursor % len(keys)
		now := s.o.Now()
		dates := guideGridDates(now, now, now.UTC().AddDate(0, 0, 1))
		n := min(len(keys), budget/len(dates))
		for i := 0; i < n; i++ {
			k := keys[(start+i)%len(keys)]
			ps, e := s.guideGrids(ctx, k, dates, &budget)
			if e != nil {
				return Fetched{}, e
			}
			g.Programmes = append(g.Programmes, ps...)
		}
		src.PlexCursor = (start + n) % len(keys)
	}
	g.Programmes = uniqueGuideProgrammes(g.Programmes)
	return Fetched{Guide: g}, nil
}

type guidePreviewEntry struct {
	at         time.Time
	programmes []Programme
}

func (s *Service) previewProgrammes(ctx context.Context, c Channel, from, until time.Time) ([]Programme, error) {
	src, err := s.storedSourceByID(ctx, guideSource(c))
	if err != nil {
		return s.programmes(ctx, c, from, until)
	}
	if !s.sourceGuideFeatures(src).Remote || c.GuideID == "" {
		return s.programmes(ctx, c, from, until)
	}
	var exists int
	err = s.o.DB.QueryRowContext(ctx, `SELECT 1 FROM livetv_guide_channels WHERE source_id=? AND guide_id=?`, src.ID, c.GuideID).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("unknown Plex guide channel")
	}
	dates := guideGridDates(s.o.Now(), from, until)
	dateKey := make([]string, 0, len(dates))
	for _, date := range dates {
		dateKey = append(dateKey, date.Format("2006-01-02"))
	}
	key := src.ID + "/" + c.GuideID + "/" + strings.Join(dateKey, ",")
	s.guidePreviewMu.Lock()
	defer s.guidePreviewMu.Unlock()
	entry, ok := s.guidePreview[key]
	if !ok || time.Since(entry.at) > 10*time.Minute {
		budget := len(dates)
		ps, e := s.guideGrids(ctx, c.GuideID, dates, &budget)
		if e != nil {
			return nil, e
		}
		if s.guidePreview == nil || len(s.guidePreview) >= 1000 {
			s.guidePreview = map[string]guidePreviewEntry{}
		}
		entry = guidePreviewEntry{at: time.Now(), programmes: ps}
		s.guidePreview[key] = entry
	}
	out := []Programme{}
	for _, p := range entry.programmes {
		if p.End.After(from) && p.Start.Before(until) {
			p.ChannelID = c.ID
			p.ID = fmt.Sprintf("%s-%d", c.ID, p.Start.UnixMilli())
			out = append(out, p)
		}
	}
	return out, nil
}
