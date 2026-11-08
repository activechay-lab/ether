package catalog

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// The /discover walk reads every title a discover query matches, without
// TMDB's cap of 500 pages (10,000 results) a query. A date range whose
// results exceed that is split, newest first: decades, years, months,
// weeks, days, and a day that is still too big by vote-count ranges. Each
// reachable slice is then read page by page. Dates are inclusive at both
// ends (primary_release_date / first_air_date .gte and .lte) and the
// children of a range partition it exactly, so no title is read twice or
// missed at a slice boundary.
//
// /discover cannot see undated titles: no date filter matches them. Only
// the daily export (sync_export.go) lists those; the walk is the fill's
// bulk reader and the fallback when no export is available.

// discoverMaxPages is the last page /discover serves.
const discoverMaxPages = 500

// discoverReachable is the most results a query can page through. TMDB
// reports total_results capped at 20,001, so the split decision compares
// with this, never with the exact count.
const discoverReachable = discoverMaxPages * 20

// walkSpec is one walk: the query and which titles it stores.
type walkSpec struct {
	tv     bool
	pass   string // "popularity" or "votes": the sort and the resume key
	params tmdb.DiscoverParams
	// stopBelow ends a popularity-sorted slice at the first title under
	// both bars: nothing later in it meets the popularity bar, and the
	// vote walk reads the vote-qualified rest.
	stopBelow bool
	// keep picks the titles the walk passes on; nil is the browse bar.
	keep func(core.Title) bool
}

// walkSink receives a walk's titles: the sync's writer, or the tail's
// names collector.
type walkSink interface {
	add(ctx context.Context, t core.Title, browsable bool) error
	pending() int
	flush(ctx context.Context) error
	setFlushHook(func(context.Context) error)
}

// walkCursor is a walk's resume point: every slice starting on or after
// Done is read, and Leaf is read up to (not including) Page.
type walkCursor struct {
	Done string `json:"done,omitempty"`
	Leaf string `json:"leaf,omitempty"`
	Page int    `json:"page,omitempty"`
	// Finished holds the read vote ranges of the day in progress.
	Finished map[string]bool `json:"finished,omitempty"`
}

// voteRange bounds vote_count; hi<0 is unbounded.
type voteRange struct{ lo, hi int }

var allVotes = voteRange{0, -1}

// dayVoteSplits partitions vote counts for a single day too big to read.
var dayVoteSplits = []voteRange{{1000, -1}, {100, 999}, {10, 99}, {1, 9}, {0, 0}}

type discoverSlice struct {
	from, to time.Time // UTC days, inclusive
	votes    voteRange
}

func (s discoverSlice) key(spec walkSpec) string {
	return fmt.Sprintf("%t/%s/%s..%s/%d-%d", spec.tv, spec.pass, s.from.Format("2006-01-02"), s.to.Format("2006-01-02"), s.votes.lo, s.votes.hi)
}

func (s discoverSlice) params(base tmdb.DiscoverParams, page int) tmdb.DiscoverParams {
	p := base
	p.Year = 0
	p.Page = page
	p.ReleaseDateGTE = s.from.Format("2006-01-02")
	p.ReleaseDateLTE = s.to.Format("2006-01-02")
	p.FirstAirGTE, p.FirstAirLTE = "", ""
	if s.votes.lo > p.VoteCountGTE {
		p.VoteCountGTE = s.votes.lo
	}
	if s.votes.hi >= 0 {
		extra := make(map[string][]string, len(p.Extra)+1)
		for k, v := range p.Extra {
			extra[k] = v
		}
		extra["vote_count.lte"] = []string{strconv.Itoa(s.votes.hi)}
		p.Extra = extra
	}
	return p
}

// split partitions a slice into children, newest first. A single day
// splits by votes; nil means it cannot be split further.
func (s discoverSlice) split(minVotes int) []discoverSlice {
	var out []discoverSlice
	add := func(from, to time.Time) {
		if from.Before(s.from) {
			from = s.from
		}
		if to.After(s.to) {
			to = s.to
		}
		if !from.After(to) {
			out = append(out, discoverSlice{from: from, to: to, votes: s.votes})
		}
	}
	y0, y1 := s.from.Year(), s.to.Year()
	switch {
	case y1-y0 >= 10:
		for d := y1 - y1%10; d+9 >= y0; d -= 10 {
			add(time.Date(d, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(d+9, 12, 31, 0, 0, 0, 0, time.UTC))
		}
	case y1 > y0:
		for y := y1; y >= y0; y-- {
			add(time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(y, 12, 31, 0, 0, 0, 0, time.UTC))
		}
	case s.from.Month() != s.to.Month():
		for m := s.to.Month(); m >= s.from.Month(); m-- {
			first := time.Date(y0, m, 1, 0, 0, 0, 0, time.UTC)
			add(first, first.AddDate(0, 1, -1))
		}
	case s.to.Sub(s.from) >= 7*24*time.Hour:
		for end := s.to; !end.Before(s.from); end = end.AddDate(0, 0, -7) {
			add(end.AddDate(0, 0, -6), end)
		}
	case s.to.After(s.from):
		for d := s.to; !d.Before(s.from); d = d.AddDate(0, 0, -1) {
			add(d, d)
		}
	case s.votes == allVotes:
		for _, v := range dayVoteSplits {
			if v.hi >= 0 && v.hi < minVotes {
				continue // below the query's own vote floor: always empty
			}
			out = append(out, discoverSlice{from: s.from, to: s.to, votes: v})
		}
	}
	return out
}

// walkRange is the full date range: syncYears, else 1800 to two years
// ahead (TMDB lists films from the 1870s and dated announcements).
func (c *Catalog) walkRange() discoverSlice {
	from, to := c.syncYears[0], c.syncYears[1]
	if from <= 0 {
		from = 1800
	}
	if to <= 0 {
		to = c.now().Year() + 2
	}
	return discoverSlice{from: time.Date(from, 1, 1, 0, 0, 0, 0, time.UTC), to: time.Date(to, 12, 31, 0, 0, 0, 0, time.UTC), votes: allVotes}
}

type discoverPage struct {
	titles     []core.Title
	totalPages int
	total      int
}

func (c *Catalog) discoverPage(ctx context.Context, tv bool, p tmdb.DiscoverParams) (discoverPage, error) {
	var out discoverPage
	if tv {
		res, err := c.tmdb().DiscoverTV(ctx, p)
		if err != nil {
			return out, err
		}
		out.totalPages, out.total = res.TotalPages, res.TotalResults
		for _, s := range res.Results {
			out.titles = append(out.titles, c.tvToTitle(ctx, s))
		}
		return out, nil
	}
	res, err := c.tmdb().DiscoverMovies(ctx, p)
	if err != nil {
		return out, err
	}
	out.totalPages, out.total = res.TotalPages, res.TotalResults
	for _, m := range res.Results {
		if m.Video && !includeVideoReleases {
			continue
		}
		out.titles = append(out.titles, c.movieToTitle(ctx, m))
	}
	return out, nil
}

// walkDiscover reads one walk to the end, storing the titles that meet
// the browse bar through w. The resume point moves only past stored pages.
func (c *Catalog) walkDiscover(ctx context.Context, spec walkSpec, w walkSink) (err error) {
	r, _ := ctx.Value(syncResumeKey{}).(*syncResume)
	passKey := fmt.Sprintf("%t/%s", spec.tv, spec.pass)
	var cur walkCursor
	if r != nil {
		if r.Passes[passKey] {
			return nil
		}
		if r.Walks == nil {
			r.Walks = map[string]walkCursor{}
		}
		cur = r.Walks[passKey]
	}
	// The cursor saved with a batch describes what that batch completes.
	saved := cur
	keep := spec.keep
	if keep == nil {
		keep = c.meetsThreshold
	}
	w.setFlushHook(func(ctx context.Context) error {
		if r == nil {
			return nil
		}
		r.Walks[passKey] = saved
		return c.saveSyncResume(ctx)
	})
	defer w.setFlushHook(nil)
	typ := "movie"
	if spec.tv {
		typ = "tv"
	}
	var walk func(s discoverSlice) error
	walk = func(s discoverSlice) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		from := s.from.Format("2006-01-02")
		key := s.key(spec)
		if cur.Done != "" && from >= cur.Done || cur.Finished[key] {
			return nil // read by an earlier attempt
		}
		page := 1
		if cur.Leaf == key && cur.Page > 1 {
			page = cur.Page
		}
		first, err := c.discoverPage(ctx, spec.tv, s.params(spec.params, page))
		if err != nil {
			return fmt.Errorf("catalog: discover %s %s page %d: %w", typ, key, page, err)
		}
		if first.totalPages > discoverMaxPages || first.total > discoverReachable {
			reachable := false
			if spec.stopBelow {
				// A popularity-sorted slice stops early: if the last
				// reachable page is already under the bars, it fits.
				last, err := c.discoverPage(ctx, spec.tv, s.params(spec.params, discoverMaxPages))
				if err != nil {
					return fmt.Errorf("catalog: discover %s %s page %d: %w", typ, key, discoverMaxPages, err)
				}
				reachable = len(last.titles) > 0 && c.belowBars(last.titles[len(last.titles)-1])
			}
			if !reachable {
				if children := s.split(spec.params.VoteCountGTE); len(children) > 0 {
					for _, child := range children {
						if err := walk(child); err != nil {
							return err
						}
					}
					cur.Done, cur.Leaf, cur.Page, cur.Finished = from, "", 0, nil
					saved = cur
					return nil
				}
				c.log.Warn("catalog: discover slice exceeds what TMDB pages; reading its first 10,000", "slice", key, "results", first.total)
			}
		}
		c.setProgress(func(p *SyncProgress) { p.Phase = typ + "/" + spec.pass + "/" + from })
		res := first
		for {
			stop := false
			for _, t := range res.titles {
				if spec.stopBelow && c.belowBars(t) {
					stop = true
					break
				}
				if keep(t) {
					if err := w.add(ctx, t, true); err != nil {
						return fmt.Errorf("catalog: store %s %s before page %d: %w", typ, key, page, err)
					}
				}
			}
			c.setProgress(func(p *SyncProgress) {
				p.PagesDone++
				p.TitlesSeen += len(res.titles)
			})
			saved = walkCursor{Done: cur.Done, Leaf: key, Page: page + 1}
			if stop || page >= min(res.totalPages, discoverMaxPages) || len(res.titles) == 0 {
				break
			}
			page++
			if res, err = c.discoverPage(ctx, spec.tv, s.params(spec.params, page)); err != nil {
				// The pages already read are stored; the resume point
				// moves past them.
				if w.pending() > 0 && ctx.Err() == nil {
					if ferr := w.flush(ctx); ferr != nil {
						c.log.Warn("catalog: store synced pages", "err", ferr)
					}
				}
				return fmt.Errorf("catalog: discover %s %s page %d: %w", typ, key, page, err)
			}
		}
		// A day's vote ranges share its date: the day is done only when
		// its last range is (the split branch above).
		if s.votes == allVotes {
			cur.Done = from
		} else {
			if cur.Finished == nil {
				cur.Finished = map[string]bool{}
			}
			cur.Finished[key] = true
		}
		cur.Leaf, cur.Page = "", 0
		saved = cur
		return nil
	}
	if err := walk(c.walkRange()); err != nil {
		return err
	}
	if err := w.flush(ctx); err != nil {
		return err
	}
	if r != nil {
		r.Passes[passKey] = true
		delete(r.Walks, passKey)
		return c.saveSyncResume(ctx)
	}
	return nil
}

// belowBars reports a title under both the popularity and the vote bar:
// in a popularity-sorted slice every later title is too, on popularity.
func (c *Catalog) belowBars(t core.Title) bool {
	cfg := c.config()
	return cfg.MinPopularity > 0 && t.Popularity < cfg.MinPopularity && (cfg.MinVotes <= 0 || t.VoteCount < cfg.MinVotes)
}
