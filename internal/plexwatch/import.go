package plexwatch

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plexguid"
	"github.com/braedonsaunders/ether/internal/state"
)

// The sweep walks verified movie and show identities in key order (movie
// keys sit below show keys). Episodes are read only for shows whose watched
// episode count or last view moved since the previous read, so the cost of
// a full sweep follows the number of movies and shows Ether has matched,
// not the number of episodes.
var (
	sweepAfter = core.MovieKey(0)
	sweepBelow = core.SeasonKey(0, 0)
)

// importPass reads one bounded slice of the viewer's Plex account state.
// Episodes of shows queued by a sweep page are read before the next page.
// An empty cursor means the last sweep finished: a pass that starts there
// with expansions still queued only finishes those, and the next sweep
// starts with the next scheduled pass. finished is true when the sweep
// wrapped (or was idle) and no expansion is left.
func (s *Service) importPass(ctx context.Context, acct accountRow) (bool, error) {
	if s.guid == nil {
		return true, nil
	}
	budget := s.importBudget
	// drain reads queued show expansions while budget lasts. It reports
	// whether any expansion is still queued.
	drain := func() (bool, error) {
		for budget > 0 {
			requested, more, err := s.expandStep(ctx, acct)
			if err != nil || !more {
				return false, err
			}
			if requested {
				budget--
				if err := s.wait(ctx); err != nil {
					return true, err
				}
			}
		}
		var n int
		err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plexwatch_expand WHERE user_id=?`, acct.userID).Scan(&n)
		return n > 0, err
	}
	var queued int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plexwatch_expand WHERE user_id=?`, acct.userID).Scan(&queued); err != nil {
		return false, err
	}
	if remain, err := drain(); err != nil || remain {
		return false, err
	}
	if acct.cursor == "" && queued > 0 {
		return true, nil
	}
	cursor := sweepAfter
	if n, err := strconv.ParseInt(acct.cursor, 10, 64); err == nil && core.RatingKey(n) > cursor {
		cursor = core.RatingKey(n)
	}
	for budget > 0 {
		page, err := s.guid.VerifiedRange(ctx, cursor, sweepBelow, maxStateBatch)
		if err != nil {
			return false, err
		}
		if len(page) == 0 {
			return true, s.saveCursor(ctx, acct, "", true)
		}
		requested, err := s.importBatch(ctx, acct, page)
		if err != nil {
			return false, err
		}
		cursor = page[len(page)-1].Key
		if err := s.saveCursor(ctx, acct, strconv.FormatInt(int64(cursor), 10), false); err != nil {
			return false, err
		}
		if requested {
			budget--
			if err := s.wait(ctx); err != nil {
				return false, err
			}
		}
		if remain, err := drain(); err != nil || remain {
			return false, err
		}
	}
	return false, nil
}

func (s *Service) wait(ctx context.Context) error {
	if s.pace <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(s.pace)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// saveCursor persists the sweep position for the generation that read it,
// so a reconnect or disconnect during a pass cannot be overwritten.
func (s *Service) saveCursor(ctx context.Context, acct accountRow, cursor string, wrapped bool) error {
	q := `UPDATE plexwatch_account SET import_cursor=?, updated_at=? WHERE user_id=? AND token_generation=? AND revoked=0`
	args := []any{cursor, s.now().UnixNano(), acct.userID, acct.tokenGeneration}
	if wrapped {
		q = `UPDATE plexwatch_account SET import_cursor=?, updated_at=?, last_sweep=? WHERE user_id=? AND token_generation=? AND revoked=0`
		args = []any{cursor, s.now().UnixNano(), s.now().Unix(), acct.userID, acct.tokenGeneration}
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return errStaleGeneration
	}
	return nil
}

// expandStep reads the next page of episodes for the first queued show.
func (s *Service) expandStep(ctx context.Context, acct accountRow) (requested, more bool, err error) {
	var show, after int64
	err = s.db.QueryRowContext(ctx, `SELECT show_key, after_key FROM plexwatch_expand WHERE user_id=? ORDER BY show_key LIMIT 1`, acct.userID).Scan(&show, &after)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	tmdb := core.RatingKey(show).TMDB()
	lo, hi := core.EpisodeKey(tmdb, 0, 0)-1, core.EpisodeKey(tmdb+1, 0, 0)
	if core.RatingKey(after) > lo {
		lo = core.RatingKey(after)
	}
	page, err := s.guid.VerifiedRange(ctx, lo, hi, maxStateBatch)
	if err != nil {
		return false, false, err
	}
	if len(page) == 0 {
		_, err = s.db.ExecContext(ctx, `DELETE FROM plexwatch_expand WHERE user_id=? AND show_key=? AND after_key=?`, acct.userID, show, after)
		return false, true, err
	}
	requested, err = s.importBatch(ctx, acct, page)
	if err != nil {
		return requested, false, err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE plexwatch_expand SET after_key=? WHERE user_id=? AND show_key=? AND after_key=?`,
		int64(page[len(page)-1].Key), acct.userID, show, after)
	return requested, true, err
}

type remoteRow struct {
	watched    bool
	viewedAt   int64
	rating     int
	leaves     int
	observedAt time.Time
	ours       bool
}

// importBatch reads one batch of userState and applies it. requested is
// false when the page held nothing that could be asked about.
func (s *Service) importBatch(ctx context.Context, acct accountRow, page []plexguid.Verified) (bool, error) {
	byHex := make(map[string]core.RatingKey, len(page))
	keys := make([]string, 0, len(page))
	for _, v := range page {
		if v.GUID == "" || !guidKindMatches(v.GUID, v.Key) {
			continue
		}
		switch v.Key.Kind() {
		case core.KindMovie, core.KindShow, core.KindEpisode:
		default:
			continue
		}
		h := guidHex(v.GUID)
		if h == "" {
			continue
		}
		if _, dup := byHex[h]; dup {
			continue
		}
		byHex[h] = v.Key
		keys = append(keys, h)
	}
	if len(keys) == 0 {
		return false, nil
	}
	states, err := s.client.userStates(ctx, acct.token, keys)
	if err != nil {
		return true, err
	}
	prev, err := s.loadRemote(ctx, acct.userID, byHex)
	if err != nil {
		return true, err
	}
	for _, h := range keys {
		cur, ok := states[h]
		if !ok {
			continue // no state reported: nothing is known, nothing changes
		}
		key := byHex[h]
		if !typeMatches(cur.Type, key) {
			continue
		}
		p, seen := prev[key]
		if key.Kind() == core.KindShow {
			err = s.observeShow(ctx, acct.userID, key, p, seen, cur)
		} else {
			err = s.applyRemote(ctx, acct.userID, key, p, seen, cur)
		}
		if err != nil {
			return true, err
		}
	}
	return true, nil
}

func typeMatches(typ string, key core.RatingKey) bool {
	switch typ {
	case "":
		return true
	case "movie":
		return key.Kind() == core.KindMovie
	case "show":
		return key.Kind() == core.KindShow
	case "episode":
		return key.Kind() == core.KindEpisode
	default:
		return false
	}
}

func (s *Service) loadRemote(ctx context.Context, userID int64, byHex map[string]core.RatingKey) (map[core.RatingKey]remoteRow, error) {
	out := make(map[core.RatingKey]remoteRow, len(byHex))
	if len(byHex) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(byHex)+1)
	args = append(args, userID)
	marks := make([]string, 0, len(byHex))
	for _, k := range byHex {
		args = append(args, int64(k))
		marks = append(marks, "?")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT rating_key, watched, viewed_at, rating, leaves, observed_at, ours FROM plexwatch_remote
		WHERE user_id=? AND rating_key IN (`+strings.Join(marks, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, observed int64
		var watched, ours int
		var r remoteRow
		if err := rows.Scan(&key, &watched, &r.viewedAt, &r.rating, &r.leaves, &observed, &ours); err != nil {
			return nil, err
		}
		r.watched, r.ours = watched != 0, ours != 0
		r.observedAt = time.Unix(0, observed)
		out[core.RatingKey(key)] = r
	}
	return out, rows.Err()
}

// observeShow queues the show's episodes for reading when its watched
// episode count or last view moved since the previous read.
func (s *Service) observeShow(ctx context.Context, userID int64, key core.RatingKey, prev remoteRow, seen bool, cur userState) error {
	changed := cur.ViewedLeaf > 0 || cur.ViewedAt > 0
	if seen {
		changed = prev.leaves != cur.ViewedLeaf || prev.viewedAt != cur.ViewedAt
	}
	if changed {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO plexwatch_expand(user_id, show_key, after_key) VALUES(?,?,0)
			ON CONFLICT(user_id, show_key) DO UPDATE SET after_key=0`, userID, int64(key)); err != nil {
			return err
		}
	}
	if !seen && cur.ViewedLeaf == 0 && cur.ViewedAt == 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO plexwatch_remote(user_id, rating_key, viewed_at, leaves, observed_at) VALUES(?,?,?,?,?)
		ON CONFLICT(user_id, rating_key) DO UPDATE SET viewed_at=excluded.viewed_at, leaves=excluded.leaves, observed_at=excluded.observed_at`,
		userID, int64(key), cur.ViewedAt, cur.ViewedLeaf, s.now().UnixNano())
	return err
}

// applyRemote brings one Plex change into Ether. Only a change since the
// previous read counts, and the newest change wins per aspect:
//
//   - watched on Plex: Plex's lastViewedAt competes with the local watch
//     clock in state.ApplyRemote.
//   - unwatched or rating changed on Plex: Plex records no time for these,
//     so the change happened after the previous read. A local change after
//     that read wins; otherwise Plex's change applies now.
//   - a value Ether wrote itself (ours) is adopted, not applied.
//
// Imported changes are marked as account events, which the journal never
// queues for sending, so nothing echoes back to Plex.
func (s *Service) applyRemote(ctx context.Context, userID int64, key core.RatingKey, prev remoteRow, seen bool, cur userState) error {
	now := s.now()
	local, err := s.localWatch(ctx, userID, key)
	if err != nil {
		return err
	}
	var watched *bool
	var watchAt time.Time
	on, off := true, false
	switch {
	case !seen:
		if cur.Watched {
			watched, watchAt = &on, unixOrZero(cur.ViewedAt)
		}
	case prev.ours && cur.Watched == prev.watched:
	case cur.Watched && (!prev.watched || cur.ViewedAt > prev.viewedAt):
		watched, watchAt = &on, unixOrZero(cur.ViewedAt)
	case !cur.Watched && prev.watched:
		if local.exists && local.watched && !local.watchAt.After(prev.observedAt) {
			watched, watchAt = &off, now
		}
	}
	var rating *float64
	var ratingAt time.Time
	switch {
	case !seen:
		if cur.Rating > 0 && local.rating == 0 {
			v := float64(cur.Rating)
			rating, ratingAt = &v, now
		}
	case prev.ours && cur.Rating == prev.rating:
	case cur.Rating != prev.rating:
		if !local.ratingAt.After(prev.observedAt) && plexRating(local.rating) != cur.Rating && (cur.Rating > 0 || local.rating > 0) {
			v := float64(cur.Rating)
			rating, ratingAt = &v, now
		}
	}
	if watched != nil || rating != nil {
		err := s.state.ApplyRemote(ctx, userID, key, watched, watchAt, rating, ratingAt)
		if err != nil && !errors.Is(err, state.ErrLocalNewer) {
			return err
		}
	}
	if !seen && !cur.Watched && cur.ViewedAt == 0 && cur.Rating == 0 {
		return nil
	}
	w := 0
	if cur.Watched {
		w = 1
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO plexwatch_remote(user_id, rating_key, watched, viewed_at, rating, observed_at, ours) VALUES(?,?,?,?,?,?,0)
		ON CONFLICT(user_id, rating_key) DO UPDATE SET watched=excluded.watched, viewed_at=excluded.viewed_at, rating=excluded.rating,
		  observed_at=excluded.observed_at, ours=0`,
		userID, int64(key), w, cur.ViewedAt, cur.Rating, now.UnixNano())
	return err
}

type localState struct {
	exists   bool
	watched  bool
	rating   float64
	watchAt  time.Time
	ratingAt time.Time
}

func (s *Service) localWatch(ctx context.Context, userID int64, key core.RatingKey) (localState, error) {
	var count int
	var watchAt, ratingAt int64
	var out localState
	err := s.db.QueryRowContext(ctx, `SELECT view_count, user_rating, watch_at, rating_at FROM state_watch WHERE user_id=? AND rating_key=?`, userID, int64(key)).
		Scan(&count, &out.rating, &watchAt, &ratingAt)
	if errors.Is(err, sql.ErrNoRows) {
		return localState{}, nil
	}
	if err != nil {
		return localState{}, err
	}
	out.exists, out.watched = true, count > 0
	out.watchAt, out.ratingAt = unixNanoOrZero(watchAt), unixNanoOrZero(ratingAt)
	return out, nil
}

func unixOrZero(sec int64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

func unixNanoOrZero(ns int64) time.Time {
	if ns <= 0 {
		return time.Time{}
	}
	return time.Unix(0, ns).UTC()
}
