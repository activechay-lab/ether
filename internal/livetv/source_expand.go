package livetv

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// droppedUnavailableKey carries a running count of titles dropped as
// known-unavailable during one expand() call. expand() copies the final
// count onto Channel.DroppedUnavailable, which maybeRecheckAvailability uses
// to find a channel worth re-expanding on its next tune or guide request
// (see expand() in service.go and maybeRecheckAvailability below).
type droppedUnavailableKey struct{}

// availabilityRecheckFloor bounds how often a channel with dropped titles
// gets re-expanded. Most drops (season-0 specials with no release, ever)
// never recover, so re-expanding on literally every tune or guide request
// would cost real work for nothing; six hours keeps a recovered title from
// sitting out for long once it's cached, without paying that cost per request.
const availabilityRecheckFloor = 6 * time.Hour

// expandProgramSource runs only while saving or rebuilding background snapshots.
// Persisted Items are the sole input to the deterministic playout engine.
func (s *Service) expandProgramSource(ctx context.Context, src *ProgramSource, depth int) ([]Item, error) {
	if src.StartSeason < 0 || src.StartEpisode < 0 {
		return nil, fmt.Errorf("show starting season and episode must not be negative")
	}
	if src.Order != "" && src.Order != "order" && src.Order != "shuffle" {
		return nil, fmt.Errorf("source order must be order or shuffle")
	}
	if depth > 8 {
		return nil, fmt.Errorf("programming sources exceed eight levels")
	}
	var items []Item
	var err error
	switch src.Kind {
	case "show":
		if s.o.Catalog == nil || src.ShowKey == 0 {
			return nil, fmt.Errorf("show source needs a catalogue show")
		}
		var titles []core.Title
		titles, err = s.o.Catalog.Grandchildren(ctx, src.ShowKey)
		for _, title := range titles {
			if title.Type == core.TypeEpisode && !title.Adult && title.Runtime > 0 && (title.Season > src.StartSeason || title.Season == src.StartSeason && title.Episode >= src.StartEpisode) {
				items = append(items, itemFromTitle(title))
			}
		}
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].Season != items[j].Season {
				return items[i].Season < items[j].Season
			}
			return items[i].Episode < items[j].Episode
		})
	case "movies":
		f := core.Filter{Type: core.TypeMovie}
		if src.Filter != nil {
			f = *src.Filter
			f.Type = core.TypeMovie
		}
		if len(src.Items) > 0 && (src.Filter == nil || src.Pool <= 0) {
			for _, it := range src.Items {
				if it.ShowKey == 0 && it.MediaType != "episode" {
					items = append(items, it)
				}
			}
		} else if src.Pool > 0 && src.Collection == 0 {
			f.ExcludeGenres = append([]string(nil), src.ExcludeGenres...)
			items, err = s.expandPopularMovies(ctx, f, src.Pool)
		} else {
			items, err = s.expandQuery(ctx, src.Collection, &f)
		}
		movies := items[:0]
		for _, it := range items {
			if it.ShowKey == 0 && it.MediaType != "episode" {
				movies = append(movies, it)
			}
		}
		items = movies
	case "personal":
		if s.o.Picker == nil || s.o.InputUser == nil {
			return nil, fmt.Errorf("personal picks unavailable")
		}
		user, e := s.o.InputUser(ctx)
		if e != nil {
			return nil, e
		}
		if user == nil {
			return nil, fmt.Errorf("personal picks need a viewer")
		}
		weights := map[string]float64{}
		if src.Personal != nil {
			for _, part := range src.Personal.Ingredients {
				if part.Enabled {
					weights[part.Kind] = part.Share
				}
			}
		}
		picks, e := s.o.Picker.Picks(ctx, user, weights, 200)
		err = e
		for _, pick := range picks {
			if pick.Title != nil && !pick.Title.Adult && pick.Title.Runtime > 0 {
				items = append(items, itemFromTitle(*pick.Title))
			}
		}
	case "plugin":
		if !strings.HasPrefix(src.SegmentID, "plugin:") || s.o.Graphics == nil {
			return nil, fmt.Errorf("plugin segment unavailable")
		}
		c := s.graphicsChannel(Channel{})
		it, ok := c.pluginItem(src.SegmentID)
		if !ok {
			return nil, fmt.Errorf("plugin segment unavailable")
		}
		items = []Item{it}
	case "lineup":
		items, err = s.expandLineup(ctx, src)
	case "mix":
		parts := make([][]Item, len(src.Sources))
		weights := make([]int, len(src.Sources))
		for i := range src.Sources {
			child := &src.Sources[i]
			parts[i], err = s.expandProgramSource(ctx, child, depth+1)
			if err != nil {
				return nil, err
			}
			if child.Kind != "lineup" {
				child.Items = append([]Item(nil), parts[i]...)
			}
			weights[i] = max(1, min(20, child.Weight))
		}
		items = interleave(parts, weights)
	case "", "items", "collection", "filter":
		items, err = s.resolveProgramming(ctx, src.Items, src.Collection, src.Filter, nil, src.Watched)
	default:
		return nil, fmt.Errorf("unknown programming source %q", src.Kind)
	}
	if err != nil {
		return nil, err
	}
	if src.Kind != "" && src.Kind != "items" && src.Kind != "collection" && src.Kind != "filter" && src.Kind != "lineup" && src.Watched != "" {
		items, err = s.filterWatched(ctx, items, src.Watched)
	}
	for i := range items {
		if src.ID != "" && src.Kind != "lineup" {
			items[i].SourceID = src.ID
		}
	}
	// A title the resolver already knows is unplayable (e.g. a catalogued
	// episode with no cached or fetchable source) would otherwise sit baked
	// into the schedule forever, hitting the live broadcast's runtime
	// fallback on every single cycle instead of just once. Drop it here, at
	// build time, so a repeating channel never schedules a slot that can
	// never air.
	kept := items[:0]
	for _, it := range items {
		if !s.knownUnavailable(ctx, it.Key) {
			kept = append(kept, it)
		} else if counter, ok := ctx.Value(droppedUnavailableKey{}).(*int); ok {
			*counter++
		}
	}
	return kept, err
}

func validateProgramming(c *Channel, check func([]Item) error) error {
	var sources func([]ProgramSource, int) error
	sources = func(list []ProgramSource, depth int) error {
		if depth > 8 || len(list) > 100 {
			return fmt.Errorf("too many programming sources")
		}
		for _, src := range list {
			if src.StartDate != "" {
				if _, err := time.Parse("2006-01-02", src.StartDate); err != nil {
					return fmt.Errorf("invalid strip start date")
				}
			}
			if src.StartSeason < 0 || src.StartEpisode < 0 || src.Weight < 0 || src.Weight > 100 {
				return fmt.Errorf("invalid source start or weight")
			}
			if src.Order != "" && src.Order != "order" && src.Order != "shuffle" {
				return fmt.Errorf("invalid source order")
			}
			if src.Strip && src.Kind != "show" {
				return fmt.Errorf("strip requires a show source")
			}
			if len(src.ExcludeGenres) > 32 {
				return fmt.Errorf("too many excluded genres")
			}
			if src.Pool < 0 || src.Pool > 200 || src.Shows < 0 || src.Shows > maxLineupPool || src.RotateDays < 0 || src.RotateDays > 365 {
				return fmt.Errorf("invalid lineup size or rotation")
			}
			if err := sources(src.Sources, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := sources(c.Sources, 0); err != nil {
		return err
	}
	for _, b := range c.Blocks {
		if _, _, err := DaypartPreset(b.Preset); err != nil {
			return err
		}
		if err := sources(b.Sources, 0); err != nil {
			return err
		}
	}
	for _, p := range c.Pins {
		if err := sources(p.Sources, 0); err != nil {
			return err
		}
	}

	look := func(l *ChannelLook) error {
		if l == nil {
			return nil
		}
		switch l.BreakPlacement {
		case "", "natural", "even", "between":
		default:
			return fmt.Errorf("unknown break placement")
		}
		for moment, digest := range l.MomentAssets {
			switch moment {
			case "ident", "upnext", "tonight", "break", "back":
			default:
				return fmt.Errorf("invalid creative moment")
			}
			if digest != "" {
				if len(digest) != 64 {
					return fmt.Errorf("invalid creative asset digest")
				}
				for _, c := range digest {
					if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
						return fmt.Errorf("invalid creative asset digest")
					}
				}
			}
		}
		if l.BreakPattern != nil {
			if err := l.BreakPattern.Validate(); err != nil {
				return err
			}
			l.BreakContent = "reel"
			l.Placement = "insert"
			if l.ReelID == "" {
				return fmt.Errorf("commercial pods require a reel")
			}
		}
		return nil
	}
	if err := look(c.Look); err != nil {
		return err
	}
	for _, b := range c.Blocks {
		if err := look(b.Breaks); err != nil {
			return err
		}
	}
	for i, p := range c.Pins {
		if p.Start < 0 || p.End <= p.Start || p.End > 1440 {
			return fmt.Errorf("pinned slots must be within 00:00–24:00")
		}
		if p.Date != "" {
			if _, err := time.Parse("2006-01-02", p.Date); err != nil {
				return fmt.Errorf("special needs a date in YYYY-MM-DD format")
			}
		}
		for _, d := range p.Days {
			if d < 0 || d > 6 {
				return fmt.Errorf("pin weekday must be 0–6")
			}
		}
		if len(p.Items) == 0 {
			return fmt.Errorf("pin needs programming")
		}
		if err := check(p.Items); err != nil {
			return err
		}
		if err := look(p.Breaks); err != nil {
			return err
		}
		if err := checkPlay(p.Mode, "", "cut", p.RepeatDays, ""); err != nil {
			return err
		}
		for _, other := range c.Pins[i+1:] {
			if p.Date != other.Date {
				continue
			}
			if blocksOverlap(Block{Start: p.Start, End: p.End, Days: p.Days}, Block{Start: other.Start, End: other.End, Days: other.Days}) {
				return fmt.Errorf("pinned slots must not overlap")
			}
		}
	}
	return nil
}

// maybeRecheckAvailability kicks off a background re-expand of c when it's
// worth one: an ordinary (non-personal) virtual channel whose last expand()
// dropped a known-unavailable title, not re-checked within
// availabilityRecheckFloor. Call this from a request path that already has
// the channel in hand - a tune or a guide lookup - never from a timer: idle
// channels (including ones that will never recover) must cost nothing.
func (s *Service) maybeRecheckAvailability(c Channel) {
	if c.Kind != "virtual" || c.Personal != nil || c.DroppedUnavailable == 0 {
		return
	}
	if s.o.Now().UnixMilli()-c.LastExpandedAt < availabilityRecheckFloor.Milliseconds() {
		return
	}
	s.recheckMu.Lock()
	if s.recheckInFlight == nil {
		s.recheckInFlight = map[string]bool{}
	}
	if s.recheckInFlight[c.ID] {
		s.recheckMu.Unlock()
		return
	}
	s.recheckInFlight[c.ID] = true
	s.recheckMu.Unlock()
	go func() {
		defer func() {
			s.recheckMu.Lock()
			delete(s.recheckInFlight, c.ID)
			s.recheckMu.Unlock()
		}()
		s.recheckChannelAvailability(s.ctx, c.ID)
	}()
}

// recheckChannelAvailability re-expands one channel and writes the result
// back only if the programme keys actually changed, so a channel whose
// drops never recover doesn't churn its stored row or the guide on every
// retry - only Channel.LastExpandedAt advances, which is enough to hold off
// the next attempt until the floor passes again.
func (s *Service) recheckChannelAvailability(ctx context.Context, id string) {
	if s.channelBusy(id) {
		return // never move a programme under a viewer; a later tune retries
	}
	var showcased int
	if err := s.o.DB.QueryRowContext(ctx, `SELECT 1 FROM livetv_showcase WHERE channel_id=?`, id).Scan(&showcased); err == nil {
		return // showcaseLoop already keeps this one current
	}
	c, err := s.Channel(ctx, id)
	if err != nil || c.Kind != "virtual" || c.Personal != nil || c.DroppedUnavailable == 0 {
		return
	}
	before := channelItemKeys(c)
	if err := s.expand(ctx, &c); err != nil {
		return
	}
	c.LastExpandedAt = s.o.Now().UnixMilli()
	if err := validateChannel(&c); err != nil {
		return
	}
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	if _, err := s.o.DB.ExecContext(ctx, `UPDATE livetv_channels SET data=? WHERE id=?`, data, id); err != nil {
		return
	}
	if itemKeysEqual(before, channelItemKeys(c)) {
		// Same programming: the write above only advanced LastExpandedAt
		// (so the next attempt waits out the floor again) - nothing a
		// viewer could see actually changed, so skip invalidating caches
		// and nudging the guide.
		return
	}
	s.invalidate(id, "")
	_ = s.refreshVirtualGuideIfWarm(ctx)
}

// channelItemKeys flattens every item key a channel schedules - the
// top-level items plus each block's and pin's own items - so
// recheckChannelAvailability can tell whether a re-expand actually changed
// anything the playout engine would ever read.
func channelItemKeys(c Channel) []core.RatingKey {
	keys := make([]core.RatingKey, 0, len(c.Items))
	for _, it := range c.Items {
		keys = append(keys, it.Key)
	}
	for _, b := range c.Blocks {
		for _, it := range b.Items {
			keys = append(keys, it.Key)
		}
	}
	for _, p := range c.Pins {
		for _, it := range p.Items {
			keys = append(keys, it.Key)
		}
	}
	return keys
}

func itemKeysEqual(a, b []core.RatingKey) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
