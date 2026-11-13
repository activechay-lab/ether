package plexwatch

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Step aside reads whole Plex Media Server libraries: owners hold tens of
// thousands of movies and hundreds of thousands of episodes. The caps bound
// memory against a runaway server, never a real library; only lookups that
// may leave the machine (a title missing from the local catalogue) are
// rationed per pass.
const (
	pmsHeldPage            = 500
	pmsHeldCap             = 2_000_000
	pmsHeldItemCacheCap    = 2_000_000
	pmsHeldUncachedLookups = 500
)

// HeldShow summarizes the seasons and episodes held across step-aside PMS
// servers for one matched TV show.
type HeldShow struct {
	Matched  bool
	Seasons  map[int][]int
	Episodes int
}

// HeldSnapshot is an immutable, in-memory index of matched titles held by Plex
// Media Servers with "Step aside for titles on this server" enabled. Request
// paths consult this snapshot in O(1) without touching the database or PMS.
type HeldSnapshot struct {
	Rev      uint64
	Movies   map[core.RatingKey]struct{}
	Episodes map[core.RatingKey]struct{}
	Shows    map[core.RatingKey]HeldShow
}

// Empty reports whether no step-aside server currently holds any matched title.
func (h *HeldSnapshot) Empty() bool {
	return h == nil || (len(h.Movies) == 0 && len(h.Episodes) == 0 && len(h.Shows) == 0)
}

// HasMovie reports whether any step-aside server holds key.
func (h *HeldSnapshot) HasMovie(key core.RatingKey) bool {
	if h == nil {
		return false
	}
	_, ok := h.Movies[key]
	return ok
}

// HasEpisode reports whether any step-aside server holds key.
func (h *HeldSnapshot) HasEpisode(key core.RatingKey) bool {
	if h == nil {
		return false
	}
	_, ok := h.Episodes[key]
	return ok
}

// HasShowEpisodes reports whether any step-aside server holds at least one
// episode of showKey.
func (h *HeldSnapshot) HasShowEpisodes(showKey core.RatingKey) bool {
	if h == nil {
		return false
	}
	return h.Shows[showKey].Episodes > 0
}

// Held returns the current in-memory step-aside snapshot.
func (s *Servers) Held() *HeldSnapshot {
	if s == nil {
		return nil
	}
	return s.held.Load()
}

// HeldGeneration returns a monotonic revision that advances whenever the
// step-aside title set changes, or 0 when no step-aside titles are active.
func (s *Servers) HeldGeneration() uint64 {
	h := s.Held()
	if h.Empty() {
		return 0
	}
	return h.Rev
}

// SetStepAside turns "Step aside for titles on this server" on or off. Turning
// it off immediately drops the server's held set from memory and storage;
// turning it on runs an initial bounded sync when sync is enabled.
func (s *Servers) SetStepAside(ctx context.Context, id int64, stepAside bool) (PMSServer, error) {
	if _, err := s.loadServer(ctx, id); err != nil {
		return PMSServer{}, err
	}
	if !stepAside {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return PMSServer{}, err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `UPDATE plexwatch_pms SET step_aside=0,held_next_run=0,held_lease=0,held_failures=0 WHERE id=?`, id); err != nil {
			return PMSServer{}, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM plexwatch_pms_held WHERE server_id=?`, id); err != nil {
			return PMSServer{}, err
		}
		if err := tx.Commit(); err != nil {
			return PMSServer{}, err
		}
		if err := s.reloadHeldSnapshot(ctx); err != nil {
			return PMSServer{}, err
		}
		return s.Server(ctx, id)
	}
	nowNS := s.now().UnixNano()
	if _, err := s.db.ExecContext(ctx, `UPDATE plexwatch_pms SET step_aside=1,held_next_run=?,held_failures=0 WHERE id=?`, nowNS, id); err != nil {
		return PMSServer{}, err
	}
	if err := s.reloadHeldSnapshot(ctx); err != nil {
		return PMSServer{}, err
	}
	if s.syncEnabled() {
		if err := s.syncHeld(ctx, id); err != nil && ctx.Err() != nil {
			return PMSServer{}, err
		}
	}
	return s.Server(ctx, id)
}

func (s *Servers) runDueHeld(ctx context.Context) {
	now := s.now().UnixNano()
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM plexwatch_pms
		WHERE step_aside<>0 AND held_next_run<=? AND held_next_allowed<=? AND held_lease<=?
		ORDER BY held_next_run LIMIT ?`, now, now, now, pmsDuePerTick)
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		if err := s.syncHeld(ctx, id); err != nil && ctx.Err() == nil {
			s.log.Info("plex media server step-aside sync paused", "server", id, "err", err.Error())
		}
	}
}

type pmsHeldRow struct {
	ratingKey core.RatingKey
	showKey   core.RatingKey
	season    int
	episode   int
}

func (s *Servers) syncHeld(ctx context.Context, serverID int64) error {
	if !s.syncEnabled() {
		return nil
	}
	now := s.now()
	lease := now.Add(10 * time.Minute).UnixNano()
	res, err := s.db.ExecContext(ctx, `UPDATE plexwatch_pms SET held_lease=? WHERE id=? AND step_aside<>0 AND held_lease<=? AND held_next_allowed<=?`,
		lease, serverID, now.UnixNano(), now.UnixNano())
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return err
	}
	var failures int
	_ = s.db.QueryRowContext(ctx, `SELECT held_failures FROM plexwatch_pms WHERE id=?`, serverID).Scan(&failures)
	interval := s.serverSyncInterval(ctx, serverID)

	pctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	capped, runErr := s.runHeldPass(pctx, serverID)
	cancel()

	if ctx.Err() != nil || !s.syncEnabled() {
		cleanup, ccancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer ccancel()
		_, _ = s.db.ExecContext(cleanup, `UPDATE plexwatch_pms SET held_lease=0 WHERE id=? AND held_lease=?`, serverID, lease)
		return ctx.Err()
	}
	if ferr := s.finishHeld(ctx, serverID, lease, interval, failures, capped, runErr); ferr != nil {
		return ferr
	}
	return runErr
}

func (s *Servers) serverSyncInterval(ctx context.Context, serverID int64) int {
	var interval int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MIN(interval_min), 10) FROM plexwatch_pms_viewer WHERE server_id=? AND direction<>0`, serverID).Scan(&interval)
	if err != nil || !validPMSInterval(interval) {
		return 10
	}
	return interval
}

func (s *Servers) runHeldPass(ctx context.Context, serverID int64) (bool, error) {
	srv, err := s.loadServer(ctx, serverID)
	if err != nil {
		return false, err
	}
	if !srv.stepAside {
		return false, nil
	}
	client, err := s.clientFor(srv.baseURL)
	if err != nil {
		return false, err
	}
	client.enabled = s.syncEnabled
	token, err := s.open(srv.token, srv.machineID)
	if err != nil {
		return false, err
	}
	cached, err := s.loadCachedItems(ctx, serverID)
	if err != nil {
		return false, err
	}
	p := &pmsPair{
		serverID:    serverID,
		owner:       true,
		client:      client,
		serverToken: token,
		items:       cached,
	}
	sections, err := client.sections(ctx, token)
	if err != nil {
		return false, err
	}
	held := make(map[core.RatingKey]pmsHeldRow)
	matchedShows := make(map[core.RatingKey]struct{})
	uncachedLookups := 0
	capped := false

	scanSection := func(secKey string, libType int) error {
		for start := 0; len(held) < pmsHeldCap; start += pmsHeldPage {
			items, err := client.sectionPage(ctx, token, secKey, libType, start, pmsHeldPage)
			if err != nil {
				return err
			}
			for _, md := range items {
				if len(held) >= pmsHeldCap {
					break
				}
				key, usedUncached, err := s.matchHeldMetadata(ctx, p, md, uncachedLookups >= pmsHeldUncachedLookups)
				if err != nil {
					return err
				}
				if usedUncached {
					uncachedLookups++
				} else if uncachedLookups >= pmsHeldUncachedLookups && key == 0 {
					capped = true
				}
				if key == 0 {
					continue
				}
				switch key.Kind() {
				case core.KindMovie:
					held[key] = pmsHeldRow{ratingKey: key}
				case core.KindShow:
					held[key] = pmsHeldRow{ratingKey: key, showKey: key}
					matchedShows[key] = struct{}{}
				case core.KindEpisode:
					showKey := core.ShowKey(key.TMDB())
					held[key] = pmsHeldRow{
						ratingKey: key,
						showKey:   showKey,
						season:    key.Season(),
						episode:   key.Episode(),
					}
					if _, ok := held[showKey]; !ok && len(held) < pmsHeldCap {
						held[showKey] = pmsHeldRow{ratingKey: showKey, showKey: showKey}
					}
					// A combined file (S05E22-23, S04E08E09) is one PMS item
					// for several episodes: hold every episode it spans.
					for _, ep := range spannedEpisodes(md, key.Season(), key.Episode()) {
						extra := core.EpisodeKey(key.TMDB(), key.Season(), ep)
						if _, ok := held[extra]; !ok && len(held) < pmsHeldCap {
							held[extra] = pmsHeldRow{ratingKey: extra, showKey: showKey, season: key.Season(), episode: ep}
						}
					}
					matchedShows[showKey] = struct{}{}
				}
			}
			if err := s.flushHeldItems(ctx, p); err != nil {
				return err
			}
			if len(items) < pmsHeldPage {
				break
			}
		}
		return nil
	}

	for _, sec := range sections {
		switch sec.Type {
		case "movie":
			if err := scanSection(sec.Key, 1); err != nil {
				return false, err
			}
		case "show":
			// Match shows first so episode resolution hits the per-server cache
			// in memory without per-show /library/metadata round-trips.
			if err := scanSection(sec.Key, 2); err != nil {
				return false, err
			}
			if err := scanSection(sec.Key, 4); err != nil {
				return false, err
			}
		}
	}

	// Re-check step_aside inside the write transaction so a concurrent toggle-off
	// cannot be overwritten by an in-flight sync pass.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT step_aside FROM plexwatch_pms WHERE id=?`, serverID).Scan(&active); err != nil {
		return false, err
	}
	if active == 0 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM plexwatch_pms_held WHERE server_id=?`, serverID); err != nil {
		return false, err
	}
	batch := make([]pmsHeldRow, 0, len(held))
	for _, r := range held {
		batch = append(batch, r)
	}
	sort.Slice(batch, func(i, j int) bool { return batch[i].ratingKey < batch[j].ratingKey })
	const insertChunk = 100
	for start := 0; start < len(batch); start += insertChunk {
		part := batch[start:min(start+insertChunk, len(batch))]
		var sb strings.Builder
		sb.WriteString(`INSERT INTO plexwatch_pms_held(server_id,rating_key,show_key,season,episode) VALUES `)
		args := make([]any, 0, len(part)*5)
		for i, r := range part {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(`(?,?,?,?,?)`)
			args = append(args, serverID, int64(r.ratingKey), int64(r.showKey), r.season, r.episode)
		}
		if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if err := s.reloadHeldSnapshot(ctx); err != nil {
		return false, err
	}
	return capped, nil
}

func (s *Servers) loadCachedItems(ctx context.Context, serverID int64) (map[int64]pmsCachedItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT pms_key,rating_key,checked_at FROM plexwatch_pms_item WHERE server_id=? LIMIT ?`,
		serverID, pmsHeldItemCacheCap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]pmsCachedItem, 256)
	for rows.Next() {
		var pmsKey, rk, checked int64
		if err := rows.Scan(&pmsKey, &rk, &checked); err != nil {
			return nil, err
		}
		out[pmsKey] = pmsCachedItem{ratingKey: core.RatingKey(rk), checkedAt: checked}
	}
	return out, rows.Err()
}

// matchHeldMetadata reuses keyFromMetadata and plexwatch_pms_item so step-aside
// and watch sync share one identifier matcher and cache.
func (s *Servers) matchHeldMetadata(ctx context.Context, p *pmsPair, md pmsMetadata, skipUncached bool) (core.RatingKey, bool, error) {
	pmsKey := int64(md.RatingKey)
	if pmsKey <= 0 {
		return 0, false, nil
	}
	if item, ok := p.items[pmsKey]; ok {
		if item.ratingKey > 0 {
			return item.ratingKey, false, nil
		}
		if s.now().Sub(time.Unix(item.checkedAt, 0)) < pmsNoMatchRetry {
			return 0, false, nil
		}
	}
	// Fast path for episodes whose parent show is already matched in p.items:
	// deriving EpisodeKey(tmdb, season, episode) is pure CPU and needs no
	// external lookup even when uncached catalog lookups are capped.
	if md.Type == "episode" {
		season, episode := int(md.ParentIndex), int(md.Index)
		if show := md.showKey(); show > 0 && episodeNumbersFit(season, episode) {
			if item, ok := p.items[show]; ok && item.ratingKey > 0 {
				epKey := core.EpisodeKey(item.ratingKey.TMDB(), season, episode)
				s.cacheHeldItem(p, pmsKey, epKey)
				return epKey, false, nil
			}
		}
	}
	// A movie or show the local catalogue already stores by its TMDB id
	// matches without leaving the machine, so it is never rationed.
	if key := s.localHeldKey(ctx, md); key != 0 {
		s.cacheHeldItem(p, pmsKey, key)
		return key, false, nil
	}
	if skipUncached {
		return 0, false, nil
	}
	key, _, err := s.keyFromMetadata(ctx, p, md)
	if err != nil {
		return 0, true, err
	}
	s.cacheHeldItem(p, pmsKey, key)
	return key, true, nil
}

// localHeldKey matches a movie or show by a TMDB id the catalogue stores,
// reading only the local database.
func (s *Servers) localHeldKey(ctx context.Context, md pmsMetadata) core.RatingKey {
	stored, ok := s.catalog.(storedTitleCatalog)
	if !ok || (md.Type != "movie" && md.Type != "show") {
		return 0
	}
	for _, id := range pmsIDs(md) {
		if id.source != "tmdb" {
			continue
		}
		n, err := strconv.ParseInt(id.value, 10, 64)
		if err != nil || n <= 0 {
			continue
		}
		key, typ := core.MovieKey(n), core.TypeMovie
		if md.Type == "show" {
			key, typ = core.ShowKey(n), core.TypeShow
		}
		if st, err := stored.StoredTitle(ctx, key); err == nil && st != nil && !st.Adult && st.Type == typ {
			return key
		}
	}
	return 0
}

// cacheHeldItem records a match in memory and queues its plexwatch_pms_item
// row; flushHeldItems writes queued rows in one transaction per page.
func (s *Servers) cacheHeldItem(p *pmsPair, pmsKey int64, key core.RatingKey) {
	checked := s.now().Unix()
	p.items[pmsKey] = pmsCachedItem{ratingKey: key, checkedAt: checked}
	p.heldPending = append(p.heldPending, pmsHeldItemRow{pmsKey: pmsKey, ratingKey: int64(key), checkedAt: checked})
}

type pmsHeldItemRow struct{ pmsKey, ratingKey, checkedAt int64 }

func (s *Servers) flushHeldItems(ctx context.Context, p *pmsPair) error {
	if len(p.heldPending) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	const chunk = 200
	for start := 0; start < len(p.heldPending); start += chunk {
		part := p.heldPending[start:min(start+chunk, len(p.heldPending))]
		var sb strings.Builder
		sb.WriteString(`INSERT INTO plexwatch_pms_item(server_id,pms_key,rating_key,checked_at) VALUES `)
		args := make([]any, 0, len(part)*4)
		for i, r := range part {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(`(?,?,?,?)`)
			args = append(args, p.serverID, r.pmsKey, r.ratingKey, r.checkedAt)
		}
		sb.WriteString(` ON CONFLICT(server_id,pms_key) DO UPDATE SET rating_key=excluded.rating_key,checked_at=excluded.checked_at`)
		if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	p.heldPending = p.heldPending[:0]
	return nil
}

func (s *Servers) finishHeld(ctx context.Context, serverID, lease int64, interval, prevFailures int, capped bool, err error) error {
	now := s.now()
	if !validPMSInterval(interval) {
		interval = 10
	}
	delay := time.Duration(interval) * time.Minute
	if capped && err == nil {
		delay = time.Minute
	}
	failures, nextAllowed := 0, int64(0)
	serverProblem := ""
	var st *StatusError
	isStatus := errors.As(err, &st)
	if err != nil {
		failures = prevFailures + 1
		delay = time.Minute << min(failures-1, 8)
		switch {
		case errors.Is(err, ErrPMSToken) || (isStatus && st.Status == http.StatusUnauthorized):
			serverProblem = "token"
		case isStatus && st.Status >= 500:
			serverProblem = "server"
		case errors.Is(err, ErrPMSUnreachable):
			serverProblem = "unreachable"
		}
		if isStatus && st.RetryAfter > delay {
			delay = min(st.RetryAfter, 24*time.Hour)
		}
		nextAllowed = now.Add(delay).UnixNano()
	}
	jitter := time.Duration((serverID*17)%53) * time.Second
	next := now.Add(delay + jitter).UnixNano()
	syncedAt := int64(0)
	if err == nil {
		syncedAt = now.Unix()
	}
	if _, e := s.db.ExecContext(ctx, `UPDATE plexwatch_pms SET held_lease=0,held_failures=?,held_next_allowed=?,held_next_run=?,
		held_synced_at=CASE WHEN ?>0 THEN ? ELSE held_synced_at END
		WHERE id=? AND held_lease=?`,
		failures, nextAllowed, next, syncedAt, syncedAt, serverID, lease); e != nil {
		return e
	}
	if isStatus && st.Status == http.StatusTooManyRequests {
		if _, e := s.db.ExecContext(ctx, `UPDATE plexwatch_pms_viewer SET next_allowed=CASE WHEN next_allowed<? THEN ? ELSE next_allowed END WHERE server_id=?`,
			nextAllowed, nextAllowed, serverID); e != nil {
			return e
		}
	}
	if err == nil || serverProblem != "" {
		if _, e := s.db.ExecContext(ctx, `UPDATE plexwatch_pms SET problem=?,checked_at=CASE WHEN ?='' THEN ? ELSE checked_at END WHERE id=?`,
			serverProblem, serverProblem, now.Unix(), serverID); e != nil {
			return e
		}
	}
	return nil
}

func (s *Servers) reloadHeldSnapshot(ctx context.Context) error {
	s.heldMu.Lock()
	defer s.heldMu.Unlock()
	rows, err := s.db.QueryContext(ctx, `SELECT h.rating_key,h.show_key,h.season,h.episode
		FROM plexwatch_pms_held h
		JOIN plexwatch_pms p ON p.id=h.server_id
		WHERE p.step_aside<>0
		LIMIT ?`, pmsMaxServers*pmsHeldCap)
	if err != nil {
		return err
	}
	defer rows.Close()

	movies := map[core.RatingKey]struct{}{}
	episodes := map[core.RatingKey]struct{}{}
	shows := map[core.RatingKey]HeldShow{}
	seasonEps := map[core.RatingKey]map[int]map[int]struct{}{}

	for rows.Next() {
		var rk, sk int64
		var season, episode int
		if err := rows.Scan(&rk, &sk, &season, &episode); err != nil {
			return err
		}
		key := core.RatingKey(rk)
		switch key.Kind() {
		case core.KindMovie:
			movies[key] = struct{}{}
		case core.KindShow:
			hs := shows[key]
			hs.Matched = true
			shows[key] = hs
		case core.KindEpisode:
			showKey := core.RatingKey(sk)
			if showKey == 0 {
				showKey = core.ShowKey(key.TMDB())
			}
			if _, dup := episodes[key]; !dup {
				episodes[key] = struct{}{}
				hs := shows[showKey]
				hs.Matched = true
				hs.Episodes++
				shows[showKey] = hs
			}
			bySeason := seasonEps[showKey]
			if bySeason == nil {
				bySeason = map[int]map[int]struct{}{}
				seasonEps[showKey] = bySeason
			}
			eps := bySeason[season]
			if eps == nil {
				eps = map[int]struct{}{}
				bySeason[season] = eps
			}
			eps[episode] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for showKey, bySeason := range seasonEps {
		hs := shows[showKey]
		hs.Seasons = make(map[int][]int, len(bySeason))
		for season, eps := range bySeason {
			list := make([]int, 0, len(eps))
			for ep := range eps {
				list = append(list, ep)
			}
			sort.Ints(list)
			hs.Seasons[season] = list
		}
		shows[showKey] = hs
	}
	rev := s.heldRev.Add(1)
	if len(movies) == 0 && len(episodes) == 0 && len(shows) == 0 {
		s.held.Store(&HeldSnapshot{Rev: rev})
		return nil
	}
	s.held.Store(&HeldSnapshot{
		Rev:      rev,
		Movies:   movies,
		Episodes: episodes,
		Shows:    shows,
	})
	return nil
}

// multiEpisodeRe finds an SxxEyy episode tag followed by more episode numbers:
// "S05E22-23", "S05E22-E23", "S04E08E09", "s01e01e02e03".
var episodeNumberRe = regexp.MustCompile(`\d{1,4}`)

var multiEpisodeRe = regexp.MustCompile(`(?i)s(\d{1,3})e(\d{1,4})((?:[-_ ]?e\d{1,4}|-\d{1,4})+)`)

// spannedEpisodes lists the episodes after first that a PMS episode item's file
// name declares for season (a range "E22-23" fills the gap; a list "E08E09"
// names each). Bounded to 10 extra episodes; nothing when the file names a
// single episode or a different season.
func spannedEpisodes(md pmsMetadata, season, first int) []int {
	for _, media := range md.Media {
		for _, part := range media.Part {
			name := part.File
			if i := strings.LastIndexAny(name, `/\\`); i >= 0 {
				name = name[i+1:]
			}
			m := multiEpisodeRe.FindStringSubmatch(name)
			if m == nil {
				continue
			}
			s, _ := strconv.Atoi(m[1])
			start, _ := strconv.Atoi(m[2])
			if s != season || start != first {
				continue
			}
			nums := episodeNumberRe.FindAllString(m[3], -1)
			last := start
			var out []int
			for _, n := range nums {
				v, _ := strconv.Atoi(n)
				if v <= last || v > first+10 {
					break
				}
				for e := last + 1; e <= v; e++ {
					out = append(out, e)
				}
				last = v
			}
			return out
		}
	}
	return nil
}
