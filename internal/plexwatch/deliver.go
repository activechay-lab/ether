package plexwatch

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plexguid"
	"github.com/braedonsaunders/ether/internal/state"
)

const missingGUIDLimit = 8

// Run delivers until Close or ctx ends. It does not decide cluster
// leadership: journal claims and title locks are safe on every node.
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		s.deliverAvailable(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.closed:
			return
		case <-tick.C:
		}
	}
}

func (s *Service) deliverAvailable(ctx context.Context) {
	if s.lastCompact.IsZero() || s.now().Sub(s.lastCompact) >= time.Hour {
		s.lastCompact = s.now()
		if err := s.state.CompactSync(ctx, s.now()); err != nil && ctx.Err() == nil {
			s.log.Warn("plex watch journal compact", "err", err.Error())
		}
	}
	for {
		more, err := s.DeliverOnce(ctx)
		if err != nil && ctx.Err() == nil {
			s.log.Warn("plex watch delivery paused", "err", err.Error())
		}
		if !more || err != nil || ctx.Err() != nil {
			return
		}
	}
}

// DeliverOnce handles at most one live intent. more is true when a row was
// claimed, including rows put back for later.
func (s *Service) DeliverOnce(ctx context.Context) (bool, error) {
	now := s.now()
	attempt := s.node + ":" + newID()
	row, ok, err := s.state.ClaimSync(ctx, attempt, now, s.claimFor)
	if err != nil || !ok {
		return false, err
	}
	locked, err := s.state.LockSync(ctx, row.UserID, row.Key, row.Aspect, attempt, now, s.claimFor)
	if err != nil {
		return true, err
	}
	if !locked {
		return true, s.state.PauseSync(ctx, row.Seq, attempt, now.Add(time.Second), "contended")
	}
	defer s.state.UnlockSync(context.WithoutCancel(ctx), row.UserID, row.Key, row.Aspect, attempt)
	return true, s.deliverClaim(ctx, row, attempt)
}

// wish is what one intent asks Plex to hold for a title.
type wish struct {
	hexKey  string
	watch   bool // the watch aspect, else the rating aspect
	watched bool
	rating  int // Plex 1–10, 0 clears
}

func (s *Service) deliverClaim(ctx context.Context, row state.SyncRow, attempt string) error {
	current, err := s.state.SyncCurrent(ctx, row.Seq, attempt)
	if err != nil || !current {
		return err
	}
	acct, ok, err := s.loadAccount(ctx, row.UserID)
	if err != nil {
		return err
	}
	if !ok || acct.revoked || acct.token == "" || acct.direction&1 == 0 || acct.plexID <= 0 {
		return s.state.DeadSync(ctx, row.Seq, attempt, "disabled")
	}
	if row.ConsentGen != acct.tokenGeneration {
		return s.state.DeadSync(ctx, row.Seq, attempt, "consent")
	}
	if wait := time.Unix(0, acct.nextAllowed); wait.After(s.now()) {
		return s.state.PauseSync(ctx, row.Seq, attempt, wait, "paused")
	}
	if row.Key.Kind() != core.KindMovie && row.Key.Kind() != core.KindEpisode {
		return s.state.DeadSync(ctx, row.Seq, attempt, "guid_kind")
	}
	guid, gerr := s.lookupGUID(ctx, row.Key)
	if gerr != nil {
		if errors.Is(gerr, plexguid.ErrAmbiguous) || errors.Is(gerr, errGUIDKind) {
			return s.state.DeadSync(ctx, row.Seq, attempt, "ambiguous_guid")
		}
		if row.Attempts+1 >= missingGUIDLimit {
			return s.state.DeadSync(ctx, row.Seq, attempt, "missing_guid")
		}
		return s.state.DeferSync(ctx, row.Seq, attempt, s.now().Add(backoff(row.Attempts)), "missing_guid")
	}
	w := wish{hexKey: guidHex(guid)}
	switch row.Kind {
	case state.SyncComplete, state.SyncUnplayed:
		w.watch, w.watched = true, row.Kind == state.SyncComplete
	case state.SyncRatingKind:
		w.rating = plexRating(row.Rating)
	default:
		return s.state.DeadSync(ctx, row.Seq, attempt, "rejected")
	}

	// A row that may already have reached Plex (never_sent=0) is read back
	// first. Writes are idempotent, so the only reason not to send it again
	// is a newer change made on Plex since.
	var neverSent int
	if err := s.db.QueryRowContext(ctx, `SELECT never_sent FROM state_sync_journal WHERE seq=?`, row.Seq).Scan(&neverSent); err != nil {
		return err
	}
	if neverSent == 0 {
		states, err := s.client.userStates(ctx, acct.token, []string{w.hexKey})
		if err != nil {
			return s.sendFailed(ctx, row, attempt, acct, err, false)
		}
		if remote, ok := states[w.hexKey]; ok {
			switch reconcile(row, w, remote) {
			case remoteHas:
				return s.sendDone(ctx, row, attempt, w)
			case remoteNewer:
				// Plex changed this title after the intent; newest wins, and
				// an import (if on) brings Plex's change into Ether.
				if _, err := s.db.ExecContext(ctx, `UPDATE plexwatch_account SET send_problem='' WHERE user_id=?`, row.UserID); err != nil {
					return err
				}
				_, err := s.state.AckSync(ctx, row.Seq, attempt, s.now())
				return err
			}
		}
	}

	admitted, reason, err := s.admit(ctx, row, attempt)
	if err != nil {
		if rerr := s.state.ReleaseSync(ctx, row.Seq, attempt); rerr != nil {
			return rerr
		}
		return err
	}
	if !admitted {
		if reason == "" {
			return nil
		}
		return s.state.DeadSync(ctx, row.Seq, attempt, reason)
	}
	if w.watch {
		err = s.client.scrobble(ctx, acct.token, w.hexKey, w.watched)
	} else {
		err = s.client.rate(ctx, acct.token, w.hexKey, w.rating)
	}
	if err != nil {
		return s.sendFailed(ctx, row, attempt, acct, err, true)
	}
	return s.sendDone(ctx, row, attempt, w)
}

// admit is the consent fence. Under the viewer's fence lock it confirms
// that this attempt still owns the live row, that the row's generation is
// the current one, and that sending is on; then it records that the row
// may reach Plex (never_sent=0) before the request leaves. Turning sync
// off, disconnecting and reconnecting move the fence under the same lock,
// so once one of those commits no further request is admitted. A request
// admitted just before cannot be recalled. reason is non-empty only when
// this attempt still owns the row and must stop for good.
func (s *Service) admit(ctx context.Context, row state.SyncRow, attempt string) (bool, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, "", err
	}
	defer tx.Rollback()
	fence, err := s.lockFence(ctx, tx, row.UserID)
	fenceMissing := errors.Is(err, sql.ErrNoRows)
	if err != nil && !fenceMissing {
		return false, "", err
	}
	var claimed string
	var dead, superseded int
	var delivered, consent int64
	err = tx.QueryRowContext(ctx, `SELECT claimed_by, dead, superseded, delivered_at, consent_generation FROM state_sync_journal WHERE seq=?`, row.Seq).
		Scan(&claimed, &dead, &superseded, &delivered, &consent)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	if claimed != attempt || dead != 0 || superseded != 0 || delivered != 0 {
		return false, "", nil
	}
	if fenceMissing || consent != fence {
		return false, "consent", nil
	}
	var direction, revoked int
	var tokenGen int64
	var sealed string
	err = tx.QueryRowContext(ctx, `SELECT direction, revoked, token_generation, token FROM plexwatch_account WHERE user_id=?`, row.UserID).
		Scan(&direction, &revoked, &tokenGen, &sealed)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (direction&1 == 0 || revoked != 0 || sealed == "" || tokenGen != fence)) {
		return false, "disabled", nil
	}
	if err != nil {
		return false, "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE state_sync_journal SET never_sent=0 WHERE seq=?`, row.Seq); err != nil {
		return false, "", err
	}
	return true, "", tx.Commit()
}

type verdict int

const (
	remoteMissing verdict = iota // Plex does not hold the intent: send it
	remoteHas                    // Plex already holds it: done
	remoteNewer                  // Plex changed the title after the intent
)

// reconcile compares an intent that may already have been sent with what
// Plex holds now. Plex keeps lastViewedAt through an unscrobble, so a
// scrobble Ether sent that was then undone on Plex leaves lastViewedAt at or
// after the intent; a watch on Plex after Ether's unwatch moves it past.
func reconcile(row state.SyncRow, w wish, remote userState) verdict {
	at := row.At.Unix()
	switch {
	case !w.watch:
		if remote.Rating == w.rating {
			return remoteHas
		}
	case w.watched:
		if remote.Watched {
			return remoteHas
		}
		if remote.ViewedAt >= at {
			return remoteNewer
		}
	default:
		if !remote.Watched {
			return remoteHas
		}
		if remote.ViewedAt > at {
			return remoteNewer
		}
	}
	return remoteMissing
}

// sendDone acknowledges the intent and records what Plex now holds, marked
// as Ether's own write so the next import does not read it back as a Plex
// change. Plex's state is recorded even when a newer intent superseded this
// one during the request: it is what Plex holds.
func (s *Service) sendDone(ctx context.Context, row state.SyncRow, attempt string, w wish) error {
	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if w.watch {
		watched := 0
		if w.watched {
			watched = 1
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO plexwatch_remote(user_id, rating_key, watched, observed_at, ours) VALUES(?,?,?,?,1)
			ON CONFLICT(user_id, rating_key) DO UPDATE SET watched=excluded.watched, observed_at=excluded.observed_at, ours=1`,
			row.UserID, int64(row.Key), watched, now.UnixNano())
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO plexwatch_remote(user_id, rating_key, rating, observed_at, ours) VALUES(?,?,?,?,1)
			ON CONFLICT(user_id, rating_key) DO UPDATE SET rating=excluded.rating, observed_at=excluded.observed_at, ours=1`,
			row.UserID, int64(row.Key), w.rating, now.UnixNano())
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE plexwatch_account SET last_send=?, send_problem='' WHERE user_id=?`, now.Unix(), row.UserID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE state_sync_journal SET delivered_at=?, claimed_by='', claimed_at=0, last_error=''
		WHERE seq=? AND claimed_by=? AND superseded=0 AND dead=0 AND delivered_at=0`, now.UnixNano(), row.Seq, attempt); err != nil {
		return err
	}
	return tx.Commit()
}

// sendFailed classifies a failed read or write. 401/403 means the token is
// no longer accepted: the connection is marked for reconnect and queued
// intents stop. 400/404/422 is a refusal for this title. 429 pauses the
// viewer for Retry-After (at least a minute) and proves nothing was applied.
// Anything else (5xx, transport, timeout) may or may not have landed: the
// row keeps never_sent=0, so the next attempt reads Plex back first.
func (s *Service) sendFailed(ctx context.Context, row state.SyncRow, attempt string, acct accountRow, err error, sent bool) error {
	if ctx.Err() != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = s.state.ReleaseSync(cleanup, row.Seq, attempt)
		return ctx.Err()
	}
	var st *StatusError
	errors.As(err, &st)
	switch {
	case errors.Is(err, ErrRevoked):
		rerr := s.revoke(ctx, row.UserID, acct.tokenGeneration)
		if rerr != nil && !errors.Is(rerr, ErrRevoked) {
			return rerr
		}
		return s.state.DeadSync(ctx, row.Seq, attempt, "revoked")
	case st != nil && (st.Status == http.StatusBadRequest || st.Status == http.StatusNotFound || st.Status == http.StatusUnprocessableEntity):
		s.log.Debug("plex refused a watch-state change", "seq", row.Seq, "status", st.Status)
		return s.state.DeadSync(ctx, row.Seq, attempt, "rejected")
	case st != nil && st.Status == http.StatusTooManyRequests:
		delay := max(st.RetryAfter, time.Minute)
		s.pauseAccount(ctx, row.UserID, acct.tokenGeneration, delay, "rate_limited")
		if sent {
			if _, err := s.db.ExecContext(ctx, `UPDATE state_sync_journal SET never_sent=1 WHERE seq=? AND claimed_by=? AND delivered_at=0`, row.Seq, attempt); err != nil {
				return err
			}
		}
		return s.state.PauseSync(ctx, row.Seq, attempt, s.now().Add(delay), "rate_limited")
	default:
		delay := backoff(row.Attempts)
		if st != nil && st.RetryAfter > delay {
			delay = st.RetryAfter
		}
		s.pauseAccount(ctx, row.UserID, acct.tokenGeneration, delay, "network")
		s.log.Debug("plex watch-state send will be checked again", "seq", row.Seq, "err", err.Error())
		return s.state.DeferSync(ctx, row.Seq, attempt, s.now().Add(delay), "uncertain")
	}
}

// pauseAccount holds every send for this viewer for delay, so one 429 or
// outage is not met with the rest of the queue.
func (s *Service) pauseAccount(ctx context.Context, userID, generation int64, delay time.Duration, problem string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE plexwatch_account SET next_allowed=?, send_problem=? WHERE user_id=? AND token_generation=?`,
		s.now().Add(delay).UnixNano(), problem, userID, generation)
}

var errGUIDKind = errors.New("plexwatch: guid kind does not match the title")

func (s *Service) lookupGUID(ctx context.Context, key core.RatingKey) (string, error) {
	if s.guid == nil {
		return "", core.ErrNotFound
	}
	guid, err := s.guid.Resolve(ctx, key)
	if err != nil {
		return "", err
	}
	if guid == "" {
		return "", core.ErrNotFound
	}
	if !guidKindMatches(guid, key) || guidHex(guid) == "" {
		return "", errGUIDKind
	}
	return guid, nil
}

func guidKindMatches(guid string, key core.RatingKey) bool {
	switch {
	case strings.HasPrefix(guid, "plex://movie/"):
		return key.Kind() == core.KindMovie
	case strings.HasPrefix(guid, "plex://show/"):
		return key.Kind() == core.KindShow
	case strings.HasPrefix(guid, "plex://episode/"):
		return key.Kind() == core.KindEpisode
	default:
		return false
	}
}

// guidHex is the Plex metadata key inside a plex:// GUID, or "".
func guidHex(guid string) string {
	i := strings.LastIndexByte(guid, '/')
	if i < 0 || !strings.HasPrefix(guid, "plex://") {
		return ""
	}
	k := guid[i+1:]
	if !validHexKey(k) {
		return ""
	}
	return k
}

func backoff(attempts int) time.Duration {
	d := time.Minute
	for i := 0; i < attempts && d < time.Hour; i++ {
		d *= 2
	}
	return min(d, time.Hour)
}
