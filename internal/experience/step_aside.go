package experience

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/reqmemo"
)

// StepAsideHeld is an immutable snapshot of matched movies, shows, and episodes
// held across external servers configured to step aside.
type StepAsideHeld struct {
	Rev      uint64
	Movies   map[core.RatingKey]struct{}
	Episodes map[core.RatingKey]struct{}
	Shows    []core.RatingKey
}

// StepAsideOptions configures protocol-scoped step-aside filtering.
//
// Only surfaces whose client apps already see the external server directly (i.e.
// Plex clients when a synced Plex Media Server has "Step aside for titles on
// this server" enabled) wire StepAside; Jellyfin, Emby, and Silo viewers cannot
// see that server and remain unaffected.
type StepAsideOptions struct {
	// Held returns the current in-memory matched-title snapshot.
	Held func() StepAsideHeld
	// AiredEpisodes returns all aired non-adult EpisodeKeys for the given
	// ShowKeys from the local catalogue mirror without network calls.
	AiredEpisodes func(ctx context.Context, shows []core.RatingKey) (map[core.RatingKey][]core.RatingKey, error)
	// WriteGeneration returns the local catalogue write generation so newly
	// stored episode rows immediately invalidate cached show completeness.
	WriteGeneration func() uint64
	// State reads viewer watch progress on Ether so in-progress titles stay
	// visible and playable.
	State core.State
	// Sessions returns active playback sessions on Ether.
	Sessions func() []*core.PlaySession
	// Now returns the current clock for minute-bucketed air-date caching.
	Now func() time.Time
}

// StepAside hides movies, fully held shows/seasons, and held episodes that an
// external server already serves, while keeping partial shows visible for the
// episodes that server lacks and preserving any title already in progress on
// Ether.
//
// Request paths never scan the external server or the full catalogue: show and
// season completeness are compiled once per (heldRev, writeGen, minute) over
// only the matched shows on step-aside servers, and per-viewer in-progress
// protection is memoized per request.
type StepAside struct {
	opts StepAsideOptions

	mu     sync.RWMutex
	cached *compiledStepAside
	// refreshing is set while a background recompile runs; lastRefresh
	// spaces recompiles caused only by catalogue writes.
	refreshing  atomic.Bool
	lastRefresh atomic.Int64
	inflight    chan struct{} // closed when the running compile finishes; guarded by mu
}

// stepAsideBucket is how often show completeness is re-judged against the
// clock (episodes that have since aired), and stepAsideRefreshGap the least
// time between recompiles caused only by catalogue writes.
const (
	stepAsideBucket     = 10 * 60
	stepAsideRefreshGap = 15 * time.Second
)

type stepAsideCacheKey struct {
	heldRev  uint64
	writeGen uint64
	minute   int64
}

type compiledStepAside struct {
	key          stepAsideCacheKey
	movies       map[core.RatingKey]struct{}
	episodes     map[core.RatingKey]struct{}
	showHasEps   map[core.RatingKey]struct{}
	heldByShow   map[core.RatingKey][]core.RatingKey
	fullShows    map[core.RatingKey]struct{}
	fullSeasons  map[core.RatingKey]struct{}
	airedByShow  map[core.RatingKey][]core.RatingKey
	movieKeys    []core.RatingKey
	fullShowKeys []core.RatingKey
}

// viewerProgress tracks rating keys that must stay visible and playable for one
// viewer despite being held by a step-aside server.
//
// In-progress protection rule:
// If a viewer already has a movie or episode in their Continue Watching /
// resume state on Ether (ViewOffset > 0 with ViewCount == 0 in state_watch) or
// in a live PlaySession on Ether, Ether must never hide or interrupt it mid-watch
// — even if a synced PMS already holds or newly downloads that title. For an
// in-progress episode, its parent season and show are also kept visible so
// metadata, ancestor, and season navigation on Plex apps continue working until
// the viewer finishes or clears progress on Ether.
type viewerProgress struct {
	items   map[core.RatingKey]struct{}
	seasons map[core.RatingKey]struct{}
	shows   map[core.RatingKey]struct{}
}

func (p viewerProgress) hasItem(k core.RatingKey) bool {
	_, ok := p.items[k]
	return ok
}

func (p viewerProgress) hasSeason(k core.RatingKey) bool {
	_, ok := p.seasons[k]
	return ok
}

func (p viewerProgress) hasShow(k core.RatingKey) bool {
	_, ok := p.shows[k]
	return ok
}

// NewStepAside constructs a StepAside policy.
func NewStepAside(opts StepAsideOptions) *StepAside {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &StepAside{opts: opts}
}

// Active reports whether any step-aside server currently holds matched titles.
func (s *StepAside) Active() bool {
	if s == nil || s.opts.Held == nil {
		return false
	}
	h := s.opts.Held()
	return h.Rev != 0 && (len(h.Movies) > 0 || len(h.Episodes) > 0)
}

// Generation returns the current held-set revision, or 0 when inactive.
func (s *StepAside) Generation() uint64 {
	if s == nil || s.opts.Held == nil {
		return 0
	}
	h := s.opts.Held()
	if h.Rev == 0 || (len(h.Movies) == 0 && len(h.Episodes) == 0) {
		return 0
	}
	return h.Rev
}

// HasAnyEpisodes reports whether step-aside servers currently hold any episodes.
func (s *StepAside) HasAnyEpisodes() bool {
	if s == nil || s.opts.Held == nil {
		return false
	}
	return len(s.opts.Held().Episodes) > 0
}

// HasShowEpisodes reports whether step-aside servers hold at least one episode
// of showKey.
func (s *StepAside) HasShowEpisodes(ctx context.Context, showKey core.RatingKey) bool {
	c := s.compiled(ctx)
	if c == nil {
		return false
	}
	_, ok := c.showHasEps[showKey]
	return ok
}

// HeldEpisodesForShows returns the held episode keys belonging to shows.
func (s *StepAside) HeldEpisodesForShows(ctx context.Context, shows []core.RatingKey) []core.RatingKey {
	c := s.compiled(ctx)
	if c == nil || len(shows) == 0 {
		return nil
	}
	var out []core.RatingKey
	seen := make(map[core.RatingKey]struct{}, len(shows))
	for _, show := range shows {
		if _, dup := seen[show]; dup {
			continue
		}
		seen[show] = struct{}{}
		out = append(out, c.heldByShow[show]...)
	}
	return out
}

// WrapAvailable composes base availability filtering with step-aside filtering.
func (s *StepAside) WrapAvailable(base func(context.Context, []core.Title) []core.Title) func(context.Context, []core.Title) []core.Title {
	return func(ctx context.Context, ts []core.Title) []core.Title {
		if base != nil {
			ts = base(ctx, ts)
		}
		return s.FilterAvailable(ctx, ts)
	}
}

// FilterAvailable removes movies, fully held shows/seasons, and held episodes
// while preserving titles in progress for the request's viewer on Ether.
func (s *StepAside) FilterAvailable(ctx context.Context, ts []core.Title) []core.Title {
	if len(ts) == 0 {
		return ts
	}
	c := s.compiled(ctx)
	if c == nil {
		return ts
	}
	anyCandidate := false
	for i := range ts {
		if c.matchesRaw(&ts[i]) {
			anyCandidate = true
			break
		}
	}
	if !anyCandidate {
		return ts
	}
	prog := s.viewerProgress(ctx, core.CatalogViewerID(ctx))
	out := make([]core.Title, 0, len(ts))
	for i := range ts {
		if !c.hidden(&ts[i], prog) {
			out = append(out, ts[i])
		}
	}
	return out
}

// ExcludedKeys returns the movie or fully-held show keys that SQL browse pages
// may exclude up front for viewer, excluding anything in progress for viewer.
func (s *StepAside) ExcludedKeys(ctx context.Context, viewer *core.User, typ core.MediaType) ([]core.RatingKey, bool) {
	c := s.compiled(ctx)
	if c == nil {
		return nil, false
	}
	var viewerID int64
	if viewer != nil {
		viewerID = viewer.ID
	} else {
		viewerID = core.CatalogViewerID(ctx)
	}
	prog := s.viewerProgress(ctx, viewerID)
	switch typ {
	case core.TypeMovie:
		if len(c.movieKeys) == 0 {
			return nil, true
		}
		out := make([]core.RatingKey, 0, len(c.movieKeys))
		for _, k := range c.movieKeys {
			if !prog.hasItem(k) {
				out = append(out, k)
			}
		}
		return out, true
	case core.TypeShow, core.TypeEpisode:
		if len(c.fullShowKeys) == 0 {
			return nil, true
		}
		out := make([]core.RatingKey, 0, len(c.fullShowKeys))
		for _, k := range c.fullShowKeys {
			if !prog.hasShow(k) {
				out = append(out, k)
			}
		}
		return out, true
	default:
		return nil, true
	}
}

// AdjustUnplayedCounts subtracts held unplayed aired episodes for shows and
// seasons that remain partially visible on Ether.
func (s *StepAside) AdjustUnplayedCounts(ctx context.Context, userID int64, keys []core.RatingKey, counts map[core.RatingKey]int) (map[core.RatingKey]int, error) {
	c := s.compiled(ctx)
	if c == nil || len(counts) == 0 || userID == 0 || s.opts.State == nil {
		return counts, nil
	}
	var heldCheck []core.RatingKey
	seen := map[core.RatingKey]struct{}{}
	for _, k := range keys {
		var showKey core.RatingKey
		switch k.Kind() {
		case core.KindShow:
			showKey = k
		case core.KindSeason:
			showKey = core.ShowKey(k.TMDB())
		default:
			continue
		}
		if _, has := c.showHasEps[showKey]; !has {
			continue
		}
		for _, ep := range c.airedByShow[showKey] {
			if _, held := c.episodes[ep]; !held {
				continue
			}
			if _, dup := seen[ep]; !dup {
				seen[ep] = struct{}{}
				heldCheck = append(heldCheck, ep)
			}
		}
	}
	if len(heldCheck) == 0 {
		return counts, nil
	}
	prog := s.viewerProgress(ctx, userID)
	states, err := s.opts.State.GetMany(ctx, userID, heldCheck)
	if err != nil {
		return counts, err
	}
	subShow := map[core.RatingKey]int{}
	subSeason := map[core.RatingKey]int{}
	for _, ep := range heldCheck {
		if prog.hasItem(ep) || states[ep].ViewCount > 0 {
			continue
		}
		sk := core.ShowKey(ep.TMDB())
		subShow[sk]++
		subSeason[core.SeasonKey(ep.TMDB(), ep.Season())]++
	}
	for _, k := range keys {
		switch k.Kind() {
		case core.KindShow:
			counts[k] = max(0, counts[k]-subShow[k])
		case core.KindSeason:
			counts[k] = max(0, counts[k]-subSeason[k])
		}
	}
	return counts, nil
}

func (c *compiledStepAside) matchesRaw(t *core.Title) bool {
	switch t.Key.Kind() {
	case core.KindMovie:
		_, ok := c.movies[t.Key]
		return ok
	case core.KindShow:
		_, ok := c.fullShows[t.Key]
		return ok
	case core.KindSeason:
		if _, ok := c.fullShows[core.ShowKey(t.Key.TMDB())]; ok {
			return true
		}
		_, ok := c.fullSeasons[t.Key]
		return ok
	case core.KindEpisode:
		_, ok := c.episodes[t.Key]
		return ok
	default:
		return false
	}
}

func (c *compiledStepAside) hidden(t *core.Title, prog viewerProgress) bool {
	switch t.Key.Kind() {
	case core.KindMovie:
		_, held := c.movies[t.Key]
		return held && !prog.hasItem(t.Key)
	case core.KindShow:
		_, full := c.fullShows[t.Key]
		return full && !prog.hasShow(t.Key)
	case core.KindSeason:
		_, fullShow := c.fullShows[core.ShowKey(t.Key.TMDB())]
		_, fullSeason := c.fullSeasons[t.Key]
		return (fullShow || fullSeason) && !prog.hasSeason(t.Key)
	case core.KindEpisode:
		_, held := c.episodes[t.Key]
		return held && !prog.hasItem(t.Key)
	default:
		return false
	}
}

func (s *StepAside) compiled(ctx context.Context) *compiledStepAside {
	if s == nil || s.opts.Held == nil {
		return nil
	}
	h := s.opts.Held()
	if h.Rev == 0 || (len(h.Movies) == 0 && len(h.Episodes) == 0) {
		return nil
	}
	var writeGen uint64
	if s.opts.WriteGeneration != nil {
		writeGen = s.opts.WriteGeneration()
	}
	ck := stepAsideCacheKey{
		heldRev:  h.Rev,
		writeGen: writeGen,
		minute:   s.opts.Now().Unix() / stepAsideBucket,
	}
	s.mu.RLock()
	cur := s.cached
	s.mu.RUnlock()
	if cur != nil && cur.key == ck {
		return cur
	}
	if cur != nil {
		// Never recompile on a request: completeness reads every matched
		// show's aired episodes. Serve the last answer and refresh it in the
		// background (one at a time, spaced unless the held set changed).
		_ = s.refreshAsync(h, ck, cur.key.heldRev != ck.heldRev)
		return cur
	}
	// The first compile also runs in the background: until it lands, step
	// aside can judge direct membership immediately. Container completeness
	// stays conservative until the catalogue read finishes.
	done := s.refreshAsync(h, ck, true)
	if core.LocalResponse(ctx) {
		// Movie and episode membership is already known in this immutable
		// snapshot; only container completeness needs the catalogue read.
		return &compiledStepAside{key: ck, movies: h.Movies, episodes: h.Episodes}
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(stepAsideFirstWait):
		case <-ctx.Done():
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cached
}

// stepAsideFirstWait preserves the bounded completeness contract for callers
// that explicitly allow a wait. Local responses use membership immediately.
const stepAsideFirstWait = 250 * time.Millisecond

// Prime starts the first compile in the background so a held set is judged
// before the first request needs it.
func (s *StepAside) Prime() {
	if s == nil || s.opts.Held == nil {
		return
	}
	h := s.opts.Held()
	if h.Rev == 0 || (len(h.Movies) == 0 && len(h.Episodes) == 0) {
		return
	}
	var writeGen uint64
	if s.opts.WriteGeneration != nil {
		writeGen = s.opts.WriteGeneration()
	}
	_ = s.refreshAsync(h, stepAsideCacheKey{heldRev: h.Rev, writeGen: writeGen, minute: s.opts.Now().Unix() / stepAsideBucket}, true)
}

func (s *StepAside) refreshAsync(h StepAsideHeld, ck stepAsideCacheKey, heldChanged bool) <-chan struct{} {
	if !heldChanged && time.Since(time.Unix(0, s.lastRefresh.Load())) < stepAsideRefreshGap {
		return nil
	}
	if !s.refreshing.CompareAndSwap(false, true) {
		s.mu.RLock()
		done := s.inflight
		s.mu.RUnlock()
		return done
	}
	done := make(chan struct{})
	s.mu.Lock()
	s.inflight = done
	s.mu.Unlock()
	go func() {
		defer close(done)
		defer s.refreshing.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		s.compile(ctx, h, ck)
		s.lastRefresh.Store(time.Now().UnixNano())
	}()
	return done
}

func (s *StepAside) compile(ctx context.Context, h StepAsideHeld, ck stepAsideCacheKey) *compiledStepAside {
	showHasEps := make(map[core.RatingKey]struct{}, len(h.Shows))
	heldByShow := make(map[core.RatingKey][]core.RatingKey, len(h.Shows))
	showSet := make(map[core.RatingKey]struct{}, len(h.Shows))
	for _, sk := range h.Shows {
		if sk.Kind() == core.KindShow {
			showSet[sk] = struct{}{}
		}
	}
	for ep := range h.Episodes {
		sk := core.ShowKey(ep.TMDB())
		showHasEps[sk] = struct{}{}
		showSet[sk] = struct{}{}
		heldByShow[sk] = append(heldByShow[sk], ep)
	}
	for sk := range heldByShow {
		eps := heldByShow[sk]
		sort.Slice(eps, func(i, j int) bool { return eps[i] < eps[j] })
		heldByShow[sk] = eps
	}

	allShows := make([]core.RatingKey, 0, len(showSet))
	for sk := range showSet {
		allShows = append(allShows, sk)
	}
	sort.Slice(allShows, func(i, j int) bool { return allShows[i] < allShows[j] })

	var airedByShow map[core.RatingKey][]core.RatingKey
	var airedErr error
	if len(allShows) > 0 && s.opts.AiredEpisodes != nil {
		airedByShow, airedErr = s.opts.AiredEpisodes(ctx, allShows)
	}

	fullShows := make(map[core.RatingKey]struct{})
	fullSeasons := make(map[core.RatingKey]struct{})
	for _, showKey := range allShows {
		eps := airedByShow[showKey]
		if len(eps) == 0 {
			continue
		}
		var regTotal, regHeld int
		var specTotal, specHeld int
		seasonTotal := map[int]int{}
		seasonHeld := map[int]int{}
		for _, ep := range eps {
			season, epNum := ep.Season(), ep.Episode()
			if epNum <= 0 || season < 0 {
				continue
			}
			_, held := h.Episodes[ep]
			seasonTotal[season]++
			if held {
				seasonHeld[season]++
			}
			if season > 0 {
				regTotal++
				if held {
					regHeld++
				}
			} else {
				specTotal++
				if held {
					specHeld++
				}
			}
		}
		if (regTotal > 0 && regHeld == regTotal) || (regTotal == 0 && specTotal > 0 && specHeld == specTotal) {
			fullShows[showKey] = struct{}{}
		}
		for season, total := range seasonTotal {
			if total > 0 && seasonHeld[season] == total {
				fullSeasons[core.SeasonKey(showKey.TMDB(), season)] = struct{}{}
			}
		}
	}

	movieKeys := make([]core.RatingKey, 0, len(h.Movies))
	for k := range h.Movies {
		movieKeys = append(movieKeys, k)
	}
	sort.Slice(movieKeys, func(i, j int) bool { return movieKeys[i] < movieKeys[j] })

	fullShowKeys := make([]core.RatingKey, 0, len(fullShows))
	for k := range fullShows {
		fullShowKeys = append(fullShowKeys, k)
	}
	sort.Slice(fullShowKeys, func(i, j int) bool { return fullShowKeys[i] < fullShowKeys[j] })

	out := &compiledStepAside{
		key:          ck,
		movies:       h.Movies,
		episodes:     h.Episodes,
		showHasEps:   showHasEps,
		heldByShow:   heldByShow,
		fullShows:    fullShows,
		fullSeasons:  fullSeasons,
		airedByShow:  airedByShow,
		movieKeys:    movieKeys,
		fullShowKeys: fullShowKeys,
	}
	if airedErr == nil {
		s.mu.Lock()
		s.cached = out
		s.mu.Unlock()
	}
	return out
}

func (s *StepAside) viewerProgress(ctx context.Context, viewerID int64) viewerProgress {
	if viewerID == 0 {
		return viewerProgress{}
	}
	load := func() (viewerProgress, error) {
		p := viewerProgress{
			items:   map[core.RatingKey]struct{}{},
			seasons: map[core.RatingKey]struct{}{},
			shows:   map[core.RatingKey]struct{}{},
		}
		mark := func(k core.RatingKey) {
			switch k.Kind() {
			case core.KindMovie:
				p.items[k] = struct{}{}
			case core.KindEpisode:
				p.items[k] = struct{}{}
				p.seasons[core.SeasonKey(k.TMDB(), k.Season())] = struct{}{}
				p.shows[core.ShowKey(k.TMDB())] = struct{}{}
			}
		}
		if s.opts.State != nil {
			if rows, err := s.opts.State.InProgress(ctx, viewerID, 500); err == nil {
				for _, st := range rows {
					if st.ViewOffset > 0 && st.ViewCount == 0 {
						mark(st.Key)
					}
				}
			}
		}
		if s.opts.Sessions != nil {
			for _, sess := range s.opts.Sessions() {
				if sess != nil && sess.UserID == viewerID && sess.Key != 0 {
					mark(sess.Key)
				}
			}
		}
		return p, nil
	}
	got, _ := reqmemo.Do(ctx, "experience:step-aside-progress:"+strconv.FormatInt(viewerID, 10), load)
	return got
}
