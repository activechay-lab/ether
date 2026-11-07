package plexwatch

import (
	"context"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/state"
)

// ErrSendingOff means the viewer's direction does not send to Plex, and
// ErrReceivingOff that it does not read from Plex.
var (
	ErrSendingOff   = errors.New("plexwatch: sending to Plex is off for this viewer")
	ErrReceivingOff = errors.New("plexwatch: reading from Plex is off for this viewer")
)

// ExportCurrent queues the viewer's watched movies and episodes and their
// ratings for sending. It runs only when the viewer asks; turning sending on
// does not upload history by itself. Resume positions are not sent.
func (s *Service) ExportCurrent(ctx context.Context, userID, plexID int64) (Status, error) {
	acct, err := s.requireMatch(ctx, userID, plexID)
	if err != nil {
		return Status{}, err
	}
	if acct.direction&1 == 0 {
		return Status{}, ErrSendingOff
	}
	rows, err := s.db.QueryContext(ctx, `SELECT rating_key, view_count, user_rating, last_viewed_at, watch_at, rating_at FROM state_watch
		WHERE user_id=? AND (view_count>0 OR user_rating>0)`, userID)
	if err != nil {
		return Status{}, err
	}
	defer rows.Close()
	now := s.now()
	var intents []state.SyncRow
	for rows.Next() {
		var key, viewed, watchAt, ratingAt int64
		var count int
		var rating float64
		if err := rows.Scan(&key, &count, &rating, &viewed, &watchAt, &ratingAt); err != nil {
			return Status{}, err
		}
		rk := core.RatingKey(key)
		if rk.Kind() != core.KindMovie && rk.Kind() != core.KindEpisode {
			continue
		}
		if count > 0 {
			at := now
			switch {
			case watchAt > 0:
				at = time.Unix(0, watchAt)
			case viewed > 0:
				at = time.Unix(0, viewed)
			}
			intents = append(intents, state.SyncRow{UserID: userID, Key: rk, Aspect: state.SyncWatch, Kind: state.SyncComplete, At: at})
		}
		if plexRating(rating) > 0 {
			at := now
			if ratingAt > 0 {
				at = time.Unix(0, ratingAt)
			}
			intents = append(intents, state.SyncRow{UserID: userID, Key: rk, Aspect: state.SyncRating, Kind: state.SyncRatingKind, Rating: rating, At: at})
		}
	}
	if err := rows.Err(); err != nil {
		return Status{}, err
	}
	rows.Close()
	if err := s.state.NoteSyncIntents(ctx, intents); err != nil {
		return Status{}, err
	}
	return s.Status(ctx, userID, plexID)
}
