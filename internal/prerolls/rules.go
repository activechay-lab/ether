package prerolls

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/core"
)

// Reason codes (owner API contract), in evaluation order.
const (
	ReasonDisabled       = "disabled"
	ReasonNotReady       = "not_ready"
	ReasonUnsupportedApp = "unsupported_app"
	ReasonResumed        = "resumed"
	ReasonKind           = "kind"
	ReasonFirstEpisode   = "first_episode"
	ReasonBingeGap       = "binge_gap"
	ReasonGenre          = "genre"
	ReasonExcludedGenre  = "excluded_genre"
	ReasonCollection     = "collection"
	ReasonRating         = "rating"
	ReasonDateWindow     = "date_window"
	ReasonAudience       = "audience"
	ReasonNetwork        = "network"
	ReasonDevice         = "device"
	ReasonFrequency      = "frequency"
	ReasonLowerPriority  = "lower_priority"
	ReasonShuffledOut    = "shuffled_out"
)

// Reason is one evaluated condition.
type Reason struct {
	Code string `json:"code"`
	OK   bool   `json:"ok"`
}

// Candidate is one pre-roll's verdict for a play.
type Candidate struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Plays   bool     `json:"plays"`
	Reasons []Reason `json:"reasons"`
}

// Winner names the chosen pre-roll.
type Winner struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Decision is the outcome of one selection.
type Decision struct {
	Winner     *Winner     `json:"winner"`
	Candidates []Candidate `json:"candidates"`

	winner *Preroll
}

// unsupportedApps cannot show a pre-roll (no intros or play-queue
// support). Real plays from them never reach selection; the simulator
// explains it.
var unsupportedApps = []string{"infuse", "swiftfin", "silo", "stremio", "dlna"}

func unsupportedApp(client string) bool {
	c := strings.ToLower(client)
	for _, app := range unsupportedApps {
		if strings.Contains(c, app) {
			return true
		}
	}
	return false
}

// playContext holds the per-play facts every candidate shares, each
// loaded at most once and only when a rule needs it.
type playContext struct {
	s    *Service
	ctx  context.Context
	req  core.PrerollRequest
	kind string

	show       *core.Title
	showLoaded bool

	lastActivity       time.Time
	lastActivityLoaded bool

	coll map[string]bool

	plays       []viewerPlay
	playsLoaded bool
	titleSeen   map[int64]bool
}

// showTitle is the episode's show, for genres, rating and collections.
func (c *playContext) showTitle() *core.Title {
	if !c.showLoaded {
		c.showLoaded = true
		f := c.req.Feature
		if f != nil && f.Type == core.TypeEpisode && f.ShowKey != 0 && c.s.title != nil {
			if t, err := c.s.title(c.ctx, f.ShowKey); err == nil {
				c.show = t
			}
		}
	}
	return c.show
}

func (c *playContext) genres() []string {
	f := c.req.Feature
	if len(f.Genres) > 0 || f.Type != core.TypeEpisode {
		return f.Genres
	}
	if show := c.showTitle(); show != nil {
		return show.Genres
	}
	return nil
}

func (c *playContext) contentRating() string {
	f := c.req.Feature
	if f.ContentRating != "" || f.Type != core.TypeEpisode {
		return f.ContentRating
	}
	if show := c.showTitle(); show != nil {
		return show.ContentRating
	}
	return ""
}

// collectionKey is what owner collections list: the movie, or the
// episode's show.
func (c *playContext) collectionKey() core.RatingKey {
	f := c.req.Feature
	if f.Type == core.TypeEpisode && f.ShowKey != 0 {
		return f.ShowKey
	}
	return f.Key
}

func (c *playContext) inCollection(id string) bool {
	if v, ok := c.coll[id]; ok {
		return v
	}
	v := c.s.inColl != nil && c.s.inColl(c.ctx, id, c.collectionKey())
	if c.coll == nil {
		c.coll = map[string]bool{}
	}
	c.coll[id] = v
	return v
}

// recentActivity is when the viewer last finished or stopped a play.
func (c *playContext) recentActivity(since time.Time) time.Time {
	if c.lastActivityLoaded {
		return c.lastActivity
	}
	c.lastActivityLoaded = true
	if c.s.activity == nil || c.req.Viewer == nil {
		return time.Time{}
	}
	if h, err := c.s.activity.History(c.ctx, c.req.Viewer.ID, since, 1); err == nil && len(h) > 0 {
		c.lastActivity = h[0].ViewedAt
	}
	if p, err := c.s.activity.InProgress(c.ctx, c.req.Viewer.ID, 1); err == nil && len(p) > 0 && p[0].LastViewedAt.After(c.lastActivity) {
		c.lastActivity = p[0].LastViewedAt
	}
	return c.lastActivity
}

// viewerPlays loads the viewer's pre-roll plays inside the longest
// non-title frequency window among the candidates.
func (c *playContext) viewerPlays(window time.Duration) []viewerPlay {
	if !c.playsLoaded {
		c.playsLoaded = true
		if c.req.Viewer != nil && window > 0 {
			c.plays, _ = c.s.recentPlays(c.ctx, c.req.Viewer.ID, c.req.At.Add(-window))
		}
	}
	return c.plays
}

func (c *playContext) seenBefore(seq int64) bool {
	if c.titleSeen == nil {
		c.titleSeen = map[int64]bool{}
		if c.req.Viewer != nil {
			if seen, err := c.s.titlePlays(c.ctx, c.req.Viewer.ID, int64(c.req.Feature.Key)); err == nil {
				c.titleSeen = seen
			}
		}
	}
	return c.titleSeen[seq]
}

// evaluate runs every rule for every pre-roll in a fixed order. With full
// false it stops each candidate at its first failure (real plays); the
// simulator passes full true to explain every condition. Both use this
// one function, so a simulation predicts playback.
func (s *Service) evaluate(ctx context.Context, req core.PrerollRequest, all []Preroll, selection string, full bool) Decision {
	if req.At.IsZero() {
		req.At = s.now()
	}
	pc := &playContext{s: s, ctx: ctx, req: req}
	if req.Feature != nil {
		switch req.Feature.Type {
		case core.TypeMovie:
			pc.kind = "movie"
		case core.TypeEpisode:
			pc.kind = "episode"
		}
	}
	var window time.Duration
	for _, p := range all {
		if p.Frequency.Mode != FrequencyTitle {
			window = max(window, p.Frequency.window())
		}
	}
	dec := Decision{Candidates: make([]Candidate, 0, len(all))}
	var matches []int
	for i := range all {
		p := &all[i]
		cand := Candidate{ID: p.ID, Name: p.Name, Reasons: []Reason{}}
		ok := true
		check := func(code string, pass func() bool) {
			if !ok && !full {
				return
			}
			r := pass()
			cand.Reasons = append(cand.Reasons, Reason{Code: code, OK: r})
			ok = ok && r
		}
		s.rules(pc, p, window, check)
		cand.Plays = ok
		if ok {
			matches = append(matches, len(dec.Candidates))
		}
		dec.Candidates = append(dec.Candidates, cand)
	}
	if len(matches) == 0 {
		return dec
	}
	var pick int
	if selection == SelectShuffle {
		pick = matches[s.rand(len(matches))]
	} else {
		best := all[matches[0]].Priority
		for _, m := range matches[1:] {
			best = max(best, all[m].Priority)
		}
		var top []int
		for _, m := range matches {
			if all[m].Priority == best {
				top = append(top, m)
			}
		}
		pick = top[s.rand(len(top))]
	}
	for _, m := range matches {
		if m == pick {
			continue
		}
		code := ReasonShuffledOut
		if selection != SelectShuffle && all[m].Priority < all[pick].Priority {
			code = ReasonLowerPriority
		}
		c := &dec.Candidates[m]
		c.Plays = false
		c.Reasons = append(c.Reasons, Reason{Code: code, OK: false})
	}
	w := all[pick]
	dec.winner = &w
	dec.Winner = &Winner{ID: w.ID, Name: w.Name}
	return dec
}

// rules is the fixed rule order shared by playback and the simulator.
func (s *Service) rules(pc *playContext, p *Preroll, window time.Duration, check func(string, func() bool)) {
	req := pc.req
	r := p.Rules
	episode := pc.kind == "episode"
	check(ReasonDisabled, func() bool { return p.Enabled })
	check(ReasonNotReady, func() bool { return p.Video.State == StateReady && fileExists(s.path(p.seq, ".mp4")) })
	check(ReasonUnsupportedApp, func() bool { return !unsupportedApp(req.Client) })
	check(ReasonResumed, func() bool { return !req.Resume })
	check(ReasonKind, func() bool { return pc.kind != "" && slices.Contains(r.Kinds, pc.kind) })
	if episode && r.FirstEpisodeOnly {
		check(ReasonFirstEpisode, func() bool { return !req.AutoPlay })
	}
	// The binge gap refines "first episode only"; with that off a pre-roll
	// plays before every episode.
	if episode && r.FirstEpisodeOnly && r.BingeGapMinutes > 0 {
		check(ReasonBingeGap, func() bool {
			if req.AutoPlay {
				return false
			}
			gap := time.Duration(r.BingeGapMinutes) * time.Minute
			last := pc.recentActivity(req.At.Add(-gap))
			return last.IsZero() || req.At.Sub(last) >= gap
		})
	}
	if len(r.Genres) > 0 {
		check(ReasonGenre, func() bool { return anyFold(pc.genres(), r.Genres) })
	}
	if len(r.ExcludeGenres) > 0 {
		check(ReasonExcludedGenre, func() bool { return !anyFold(pc.genres(), r.ExcludeGenres) })
	}
	if len(r.Collections) > 0 {
		check(ReasonCollection, func() bool {
			for _, id := range r.Collections {
				if pc.inCollection(id) {
					return true
				}
			}
			return false
		})
	}
	if r.MaxContentRating != "" {
		check(ReasonRating, func() bool { return contentrating.Allowed(pc.contentRating(), r.MaxContentRating) })
	}
	if r.DateFrom != "" {
		check(ReasonDateWindow, func() bool { return inDateWindow(req.At, r.DateFrom, r.DateTo) })
	}
	if r.Audience.Mode == "only" || r.Audience.Mode == "except" {
		check(ReasonAudience, func() bool {
			listed := req.Viewer != nil && slices.Contains(r.Audience.Viewers, req.Viewer.ID)
			return listed == (r.Audience.Mode == "only")
		})
	}
	if r.Network == "home" || r.Network == "away" {
		check(ReasonNetwork, func() bool { return req.Away == (r.Network == "away") })
	}
	if len(r.Devices) > 0 {
		check(ReasonDevice, func() bool { return slices.Contains(r.Devices, req.Device) })
	}
	if p.Frequency.Mode != FrequencyAlways && p.Frequency.Mode != "" {
		check(ReasonFrequency, func() bool { return pc.frequencyOK(p, window) })
	}
}

func (c *playContext) frequencyOK(p *Preroll, window time.Duration) bool {
	if c.req.Viewer == nil {
		return true
	}
	switch p.Frequency.Mode {
	case FrequencyTitle:
		return !c.seenBefore(p.seq)
	case FrequencyDaily:
		y, m, d := c.req.At.Date()
		since := time.Date(y, m, d, 0, 0, 0, 0, c.req.At.Location())
		return !c.playedSince(p.seq, since, window)
	case FrequencyHours:
		return !c.playedSince(p.seq, c.req.At.Add(-time.Duration(p.Frequency.Hours)*time.Hour), window)
	}
	return true
}

func (c *playContext) playedSince(seq int64, since time.Time, window time.Duration) bool {
	for _, pl := range c.viewerPlays(window) {
		if pl.seq == seq && !pl.at.Before(since) && !pl.at.After(c.req.At) {
			return true
		}
	}
	return false
}

func anyFold(have, want []string) bool {
	for _, h := range have {
		for _, w := range want {
			if strings.EqualFold(strings.TrimSpace(h), w) {
				return true
			}
		}
	}
	return false
}

// inDateWindow reports whether at's month-day lies in [from,to]; a window
// whose end precedes its start wraps the new year (12-15 to 01-05).
func inDateWindow(at time.Time, from, to string) bool {
	md := at.Format("01-02")
	if from <= to {
		return md >= from && md <= to
	}
	return md >= from || md <= to
}
