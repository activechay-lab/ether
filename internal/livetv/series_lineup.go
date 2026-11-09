package livetv

import (
	"context"
	"fmt"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// A lineup is a smart series rotation: the most popular shows matching a
// filter form a pool, Shows of them are on air together, and every RotateDays
// broadcast days the next group of the pool takes over. Each series is a strip:
// it resumes at the episode after the last one it aired, also after time off air.
const (
	maxLineupPool     = 48
	lineupEpisodeCap  = 32 // a syndication package; reruns wrap after it
	defaultLineupSize = 4
)

func lineupShows(src ProgramSource) int {
	if src.Shows > 0 {
		return src.Shows
	}
	return defaultLineupSize
}

func lineupPool(src ProgramSource) int {
	pool := src.Pool
	if pool <= 0 {
		pool = lineupShows(src)
		if src.RotateDays > 0 {
			pool *= 3
		}
	}
	return min(pool, maxLineupPool)
}

// expandLineup runs only while saving or refreshing a channel. It stores each
// series as a child show source with a bounded, in-order episode list and
// returns a short preview (each series' first episode) as the source's items.
func (s *Service) expandLineup(ctx context.Context, src *ProgramSource) ([]Item, error) {
	if s.o.Catalog == nil {
		return nil, fmt.Errorf("lineup needs the catalogue")
	}
	f := core.Filter{}
	if src.Filter != nil {
		f = *src.Filter
	}
	f.Type = core.TypeShow
	f.ExcludeGenres = append([]string(nil), src.ExcludeGenres...)
	claimed, _ := ctx.Value(claimedSeriesKey{}).(map[core.RatingKey]bool)
	pool := lineupPool(*src)
	page, err := s.o.Catalog.Browse(ctx, core.BrowseQuery{Filter: f, Sort: core.SortPopularity, Desc: true, Limit: min(pool+len(claimed), 200)})
	if err != nil {
		return nil, err
	}
	previous := map[core.RatingKey]ProgramSource{}
	for _, child := range src.Sources {
		previous[child.ShowKey] = child
	}
	var children []ProgramSource
	// Unclaimed series first; a lineup short of series shares already-homed
	// ones rather than going dark.
	var candidates []core.Title
	for _, show := range page.Items {
		if show.Type == core.TypeShow && !show.Adult && !claimed[show.Key] {
			candidates = append(candidates, show)
		}
	}
	if short := lineupShows(*src) - len(candidates); short > 0 {
		for _, show := range page.Items {
			if short > 0 && show.Type == core.TypeShow && !show.Adult && claimed[show.Key] {
				candidates = append(candidates, show)
				short--
			}
		}
	}
	for _, show := range candidates {
		if len(children) == pool {
			break
		}
		episodes, err := s.lineupEpisodes(ctx, show)
		if err != nil || len(episodes) == 0 {
			// A series the catalogue cannot list today keeps its last snapshot.
			if old, ok := previous[show.Key]; ok && len(old.Items) > 0 {
				children = append(children, old)
			}
			continue
		}
		id := fmt.Sprintf("%s-%d", src.ID, show.Key)
		for i := range episodes {
			episodes[i].SourceID = id
		}
		children = append(children, ProgramSource{ID: id, Kind: "show", ShowKey: show.Key, Name: show.Title, Order: "order", Items: episodes})
	}
	// Group membership depends only on each series, not on its popularity
	// rank, so a refresh that admits one new series leaves the others' groups.
	sortLineup(children)
	for _, child := range children {
		if claimed != nil {
			claimed[child.ShowKey] = true
		}
	}
	src.Sources = children
	src.Items = nil
	var preview []Item
	for _, child := range children {
		preview = append(preview, child.Items[0])
	}
	return preview, nil
}

func sortLineup(children []ProgramSource) {
	hash := func(p ProgramSource) uint64 { return uint64(int64Hash(p.ShowKey.String())) }
	for i := 1; i < len(children); i++ {
		for j := i; j > 0 && hash(children[j]) < hash(children[j-1]); j-- {
			children[j], children[j-1] = children[j-1], children[j]
		}
	}
}

// claimedSeriesKey carries the series already given a home slot while one
// channel is expanded: a series belongs to a single lineup per channel.
type claimedSeriesKey struct{}

// lineupEpisodes lists a series' first episodes in broadcast order, reading
// seasons one at a time so a long-running show costs only what airs.
func (s *Service) lineupEpisodes(ctx context.Context, show core.Title) ([]Item, error) {
	seasons, err := s.o.Catalog.Children(ctx, show.Key)
	if err != nil {
		return nil, err
	}
	var out []Item
	for _, season := range seasons {
		if season.Season <= 0 || season.Type != core.TypeSeason {
			continue
		}
		episodes, err := s.o.Catalog.Children(ctx, season.Key)
		if err != nil {
			return out, err
		}
		for _, ep := range episodes {
			if ep.Type != core.TypeEpisode || ep.Adult || ep.Runtime <= 0 || ep.Episode <= 0 {
				continue
			}
			out = append(out, episodeItem(ep, show))
			if len(out) >= lineupEpisodeCap {
				return out, nil
			}
		}
	}
	return out, nil
}

// episodeItem carries the series name as the programme title and the episode
// name separately, the way guides and the Plex EPG present a series.
func episodeItem(ep, show core.Title) Item {
	it := itemFromTitle(ep)
	it.ShowTitle = orText(ep.ShowTitle, show.Title)
	it.EpisodeTitle = ep.Title
	it.Title = it.ShowTitle
	it.MediaType = "episode"
	it.ShowKey = show.Key
	it.Art = show.BackdropPath
	if r := []rune(it.Summary); len(r) > 240 {
		it.Summary = string(r[:239]) + "…"
	}
	if it.Thumb == "" {
		it.Thumb = show.PosterPath
	}
	if it.Rating == "" {
		it.Rating = show.ContentRating
	}
	return it
}

// expandPopularMovies draws the pool's most popular movies, in that order.
func (s *Service) expandPopularMovies(ctx context.Context, f core.Filter, pool int) ([]Item, error) {
	if s.o.Catalog == nil {
		return nil, fmt.Errorf("catalogue unavailable")
	}
	page, err := s.o.Catalog.Browse(ctx, core.BrowseQuery{Filter: f, Sort: core.SortPopularity, Desc: true, Limit: min(pool, 200)})
	if err != nil {
		return nil, err
	}
	var out []Item
	for _, t := range page.Items {
		if t.Type != core.TypeMovie || t.Adult || t.Runtime <= 0 {
			continue
		}
		it := itemFromTitle(t)
		it.MediaType = "movie"
		it.Genres = t.Genres
		it.Art = t.BackdropPath
		out = append(out, it)
	}
	return out, nil
}

// lineupDay is the lineup's running order for one broadcast day: the group on
// air in fixed slot order, each series starting at the episode it reached.
// Positions are arithmetic in the number of broadcast days a series has been
// on air, so a channel years old costs the same as a new one.
func lineupDay(src ProgramSource, c Channel, day time.Time, b Block, share float64) []Item {
	var pool []ProgramSource
	for _, child := range src.Sources {
		if len(child.Items) > 0 {
			pool = append(pool, child)
		}
	}
	if len(pool) == 0 {
		return nil
	}
	size := min(lineupShows(src), len(pool))
	groups := 1
	if src.RotateDays > 0 {
		groups = (len(pool) + size - 1) / size
	} else {
		pool = pool[:size]
	}
	epoch := dayStart(c.Epoch, day.Location())
	if src.StartDate != "" {
		if at, err := time.ParseInLocation("2006-01-02", src.StartDate, day.Location()); err == nil {
			epoch = at
		}
	}
	elapsed := broadcastDays(epoch, day, b.Days)
	group := 0
	if groups > 1 {
		group = (elapsed / src.RotateDays) % groups
	}
	minutes := float64(b.End-b.Start) * share
	var members []ProgramSource
	for i, child := range pool {
		if i%groups == group {
			members = append(members, child)
		}
	}
	perDay := lineupPerDay(members, minutes)
	var lists [][]Item
	for i, child := range members {
		onAir := elapsed
		if groups > 1 {
			cycle := src.RotateDays * groups
			onAir = (elapsed/cycle)*src.RotateDays + min(max(elapsed%cycle-group*src.RotateDays, 0), src.RotateDays)
		}
		// Each series joins mid-run at its own point, like a station's
		// syndication package, then continues episode by episode.
		offset := int(uint64(int64Hash(fmt.Sprint(c.Seed, child.ShowKey))) % uint64(len(child.Items)))
		at := (offset + onAir*perDay[i]) % len(child.Items)
		items := append(append([]Item(nil), child.Items[at:]...), child.Items[:at]...)
		lists = append(lists, items)
	}
	// Round robin in fixed slot order, as many turns as a long day needs.
	var out []Item
	for turn := 0; len(out) < 2000; turn++ {
		added := false
		for _, list := range lists {
			if turn < len(list) {
				out = append(out, list[turn])
				added = true
			}
		}
		if !added {
			break
		}
	}
	return out
}

// lineupPerDay is how many episodes each series airs in one day's block. It
// assumes each series' shortest episode, so a day of longer episodes skips an
// episode rather than repeating one.
func lineupPerDay(members []ProgramSource, minutes float64) []int {
	counts := make([]int, len(members))
	if len(members) == 0 || minutes <= 0 {
		return counts
	}
	shortest := make([]float64, len(members))
	for i, m := range members {
		shortest[i] = -1
		for _, it := range m.Items {
			d := float64(it.DurationMS) / 60000
			if d > 0 && (shortest[i] < 0 || d < shortest[i]) {
				shortest[i] = d
			}
		}
		if shortest[i] <= 0 {
			shortest[i] = 30
		}
	}
	used := 0.0
	for turn := 0; used < minutes && turn < 1000; turn++ {
		for i := range members {
			if used >= minutes {
				break
			}
			counts[i]++
			used += shortest[i]
		}
	}
	for i := range counts {
		counts[i] = max(1, counts[i])
	}
	return counts
}
