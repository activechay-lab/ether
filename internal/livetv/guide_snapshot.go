package livetv

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type virtualGuideSnapshot struct {
	recipe               string
	built                time.Time
	revision             uint64
	channel              Channel
	from, until          time.Time
	raw, playback, guide []Programme
}
type virtualGuideCache struct {
	mu       sync.RWMutex
	buildMu  sync.Mutex
	epoch    int64
	revision uint64
	entries  map[string]*virtualGuideSnapshot
	wake     chan struct{}
}

func (s *Service) wakeVirtualGuide() {
	if s.virtualGuide.wake != nil {
		select {
		case s.virtualGuide.wake <- struct{}{}:
		default:
		}
	}
}
func (s *Service) virtualGuideLoop() {
	defer s.wg.Done()
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		if err := s.refreshVirtualGuide(s.ctx); err != nil && s.ctx.Err() == nil {
			s.o.Logger.Debug("virtual guide snapshot refresh failed", "error", err)
		}
		select {
		case <-s.ctx.Done():
			return
		case <-tick.C:
		case <-s.virtualGuide.wake:
		}
	}
}
func (s *Service) refreshVirtualGuide(ctx context.Context) error {
	s.virtualGuide.buildMu.Lock()
	defer s.virtualGuide.buildMu.Unlock()
	if err := s.backfillPersonalIndex(ctx); err != nil {
		return err
	}
	var epoch int64
	if err := s.o.DB.QueryRowContext(ctx, `SELECT generation FROM livetv_channel_epoch WHERE id=1`).Scan(&epoch); err != nil {
		return err
	}
	cs, err := channels(ctx, s.o.DB)
	if err != nil {
		return err
	}
	now := s.o.Now()
	s.virtualGuide.mu.RLock()
	revision := s.virtualGuide.revision
	s.virtualGuide.mu.RUnlock()
	entries := map[string]*virtualGuideSnapshot{}
	for _, c := range cs {
		if c.Kind != "virtual" {
			continue
		}
		if c.Personal != nil {
			entries[c.ID] = &virtualGuideSnapshot{channel: c}
			continue
		}
		recipe, _ := json.Marshal(c)
		s.virtualGuide.mu.RLock()
		prior := s.virtualGuide.entries[c.ID]
		s.virtualGuide.mu.RUnlock()
		if prior != nil && prior.recipe == string(recipe) && prior.revision == revision && now.Sub(prior.built) < 30*time.Second {
			entries[c.ID] = prior
			continue
		}
		from, until := now.Add(-24*time.Hour), now.Add(8*24*time.Hour+time.Hour)
		gated := s.guideChannel(ctx, c)
		entry := &virtualGuideSnapshot{channel: c, recipe: string(recipe), built: now, from: from, until: until, raw: Schedule(c, from, until), revision: revision}
		entry.playback = entry.raw
		if !reflect.DeepEqual(c, gated) {
			entry.playback = Schedule(gated, from, until)
		}
		entry.guide = entry.playback
		if gated.clipDuration != nil {
			entry.guide = s.broadcastGuide(gated, from, until)
		}
		tags := map[struct {
			key           core.RatingKey
			rating, slate string
		}]*broadcastPolicyTag{}
		facts := map[core.RatingKey]*core.Title{}
		loaded := map[core.RatingKey]bool{}
		for _, ps := range [][]Programme{entry.raw, entry.playback, entry.guide} {
			for i := range ps {
				p := ps[i]
				if p.Item.Key != 0 && !loaded[p.Item.Key] && s.o.Catalog != nil {
					title, err := s.o.Catalog.Get(ctx, p.Item.Key)
					if err == nil && title != nil {
						copy := *title
						facts[p.Item.Key] = &copy
					}
					loaded[p.Item.Key] = true
				}
				identity := struct {
					key           core.RatingKey
					rating, slate string
				}{p.Item.Key, p.Item.Rating, p.Item.Slate}
				if tags[identity] == nil {
					tags[identity] = &broadcastPolicyTag{programme: Programme{Item: Item{Key: p.Item.Key, Rating: p.Item.Rating, Slate: p.Item.Slate}}, title: facts[p.Item.Key], catalog: s.o.Catalog != nil}
				}
				ps[i].policyTag = tags[identity]
			}
		}
		entries[c.ID] = entry
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	s.virtualGuide.mu.Lock()
	s.virtualGuide.epoch, s.virtualGuide.entries = epoch, entries
	s.virtualGuide.mu.Unlock()
	s.chanSnap.mu.Lock()
	s.chanSnap.generation, s.chanSnap.list = epoch, cs
	s.chanSnap.byID = make(map[string]int, len(cs))
	s.chanSnap.bySource = map[string][]int{}
	for i, c := range cs {
		s.chanSnap.byID[c.ID] = i
		s.chanSnap.bySource[c.SourceID] = append(s.chanSnap.bySource[c.SourceID], i)
	}
	s.chanSnap.mu.Unlock()
	return nil
}

// A binary search selects just the requested time range. Request paths never
// expand schedules, gate release availability, or hydrate titles.
func programmeSlice(ps []Programme, from, until time.Time) []Programme {
	first := sort.Search(len(ps), func(i int) bool { return ps[i].End.After(from) })
	last := first + sort.Search(len(ps)-first, func(i int) bool { return !ps[first+i].Start.Before(until) })
	return append([]Programme(nil), ps[first:last]...)
}
func (s *Service) virtualProgrammes(c Channel, from, until time.Time, kind string) []Programme {
	s.virtualGuide.mu.RLock()
	entry := s.virtualGuide.entries[c.ID]
	s.virtualGuide.mu.RUnlock()
	if entry == nil || from.Before(entry.from) || until.After(entry.until) {
		s.wakeVirtualGuide()
		return nil
	}
	ps := entry.playback
	if kind == "raw" {
		ps = entry.raw
	}
	if kind == "guide" {
		ps = entry.guide
	}
	return programmeSlice(ps, from, until)
}
func (s *Service) allowedProgramme(ctx context.Context, u *core.User, c Channel, p *Programme) bool {
	if p != nil && p.policyTag != nil {
		tag := p.policyTag
		var lookup func(context.Context, core.RatingKey) (*core.Title, error)
		if tag.catalog {
			lookup = func(context.Context, core.RatingKey) (*core.Title, error) { return tag.title, nil }
		}
		return s.allowedWithLookup(ctx, u, c, p, lookup, false)
	}
	return s.allowed(ctx, u, c, p)
}

type virtualGuideContextKey struct{}

func (s *Service) withVirtualGuide(ctx context.Context, cs []Channel) (context.Context, error) {
	if _, ok := ctx.Value(virtualGuideContextKey{}).(bool); ok {
		return ctx, nil
	}
	needed := false
	for _, c := range cs {
		if c.Kind == "virtual" && c.Enabled {
			needed = true
			break
		}
	}
	if !needed {
		return ctx, nil
	}
	var epoch int64
	if err := s.o.DB.QueryRowContext(ctx, `SELECT generation FROM livetv_channel_epoch WHERE id=1`).Scan(&epoch); err != nil {
		return ctx, err
	}
	s.virtualGuide.mu.RLock()
	ready := s.virtualGuide.epoch == epoch
	for _, c := range cs {
		if c.Kind == "virtual" && c.Enabled && s.virtualGuide.entries[c.ID] == nil {
			ready = false
		}
	}
	s.virtualGuide.mu.RUnlock()
	if !ready {
		s.wakeVirtualGuide()
		return ctx, core.ErrNotAvailable
	}
	return context.WithValue(ctx, virtualGuideContextKey{}, true), nil
}

// RefreshGuide is the owner-save hook for look and schedule changes. Viewer
// reads never call this method. The background worker also refreshes availability.
func (s *Service) RefreshGuide(ctx context.Context) error {
	s.virtualGuide.mu.Lock()
	s.virtualGuide.revision++
	s.virtualGuide.mu.Unlock()
	return s.refreshVirtualGuide(ctx)
}

// SnapshotView selects an existing immutable representation. All Programme
// fields (including source/block provenance) survive the time-indexed read.
type SnapshotView string

const (
	SnapshotRaw      SnapshotView = "raw"
	SnapshotPlayback SnapshotView = "playback"
	SnapshotGuide    SnapshotView = "guide"
)

func (s *Service) ProgrammesSnapshot(ctx context.Context, u *core.User, id string, from, until time.Time, view SnapshotView) ([]Programme, error) {
	if !until.After(from) || until.Sub(from) > 8*24*time.Hour {
		return nil, fmt.Errorf("invalid snapshot window")
	}
	if view != SnapshotRaw && view != SnapshotPlayback && view != SnapshotGuide {
		return nil, fmt.Errorf("invalid snapshot view")
	}
	ctx, _, err := s.withPolicy(ctx, u)
	if err != nil {
		return nil, err
	}
	s.virtualGuide.mu.RLock()
	entry := s.virtualGuide.entries[id]
	s.virtualGuide.mu.RUnlock()
	if entry == nil {
		s.wakeVirtualGuide()
		return nil, core.ErrNotAvailable
	}
	c := s.personalChannel(ctx, entry.channel, u)
	ctx, err = s.withVirtualGuide(ctx, []Channel{c})
	if err != nil {
		return nil, err
	}
	var ps []Programme
	if c.Personal != nil {
		ps, err = s.personalProgrammes(ctx, c, u, from, until)
	} else {
		if from.Before(entry.from) || until.After(entry.until) {
			return nil, core.ErrNotAvailable
		}
		ps = s.virtualProgrammes(c, from, until, string(view))
	}
	if err != nil {
		return nil, err
	}
	out := make([]Programme, 0, len(ps))
	for _, p := range ps {
		if s.allowedProgramme(ctx, u, c, &p) {
			out = append(out, p)
		}
	}
	return out, nil
}

func (s *Service) checkVirtualWindow(c Channel, from, until time.Time) error {
	if c.Kind != "virtual" || c.Personal != nil || !c.Enabled {
		return nil
	}
	s.virtualGuide.mu.RLock()
	entry := s.virtualGuide.entries[c.ID]
	s.virtualGuide.mu.RUnlock()
	if entry == nil || from.Before(entry.from) || until.After(entry.until) {
		s.wakeVirtualGuide()
		return core.ErrNotAvailable
	}
	return nil
}
