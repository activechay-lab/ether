package state

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"strings"
	"time"
)

func (s *State) explicitProgressAt(ctx context.Context, user int64, key core.RatingKey, w progressWrite) error {
	return s.updateAt(ctx, user, []core.RatingKey{key}, w.at, func(cur *row, now time.Time) (bool, bool, []Event) {
		complete := w.state == "explicit-complete"
		before := cur.ws.ViewOffset
		if w.duration > 0 {
			cur.ws.Duration = w.duration
		}
		switch w.state {
		case "explicit-resume":
			cur.ws.ViewOffset = w.offset
			cur.ws.LastViewedAt = now
		case "explicit-complete":
			cur.ws.ViewOffset = 0
			cur.ws.ViewCount++
			cur.ws.LastViewedAt = now
		case "explicit-clear":
			cur.ws.ViewOffset = 0
		case "explicit-offline-clear":
			cur.ws.ViewOffset = 0
			cur.ws.ViewCount = 0
		}
		cur.ws.UpdatedAt = now
		cur.watchAt = now
		kind := EventProgress
		if complete {
			kind = EventCompleted
		}
		ev := Event{Kind: kind, UserID: user, Key: key, Offset: w.offset, Duration: cur.ws.Duration, Completed: complete, State: w.timelineState, At: now}
		if complete {
			ev.Fraction = 1
		} else {
			ev.ResumeSet, ev.Resume = cur.ws.ViewOffset != before, cur.ws.ViewOffset
		}
		return complete && key.Kind() != core.KindPlaylist, complete, []Event{ev}
	})
}

// applyProgressChain retains completion events while coalescing held positions.
func (s *State) applyProgressChain(ctx context.Context, k progressKey, w progressWrite) error {
	if w.before != nil {
		if err := s.applyProgressChain(ctx, k, *w.before); err != nil {
			return err
		}
	}
	// Retrying a partially flushed chain must not append the same history again.
	if !w.at.IsZero() && strings.HasPrefix(w.state, "explicit-") {
		var at int64
		err := s.db.QueryRowContext(ctx, `SELECT watch_at FROM state_watch WHERE user_id=? AND rating_key=?`, k.user, int64(k.key)).Scan(&at)
		if err == nil && !unixTime(at).Before(w.at) {
			return nil
		}
	}
	return s.progressAt(ctx, k.user, k.key, w)
}

// ApplyWatchMutation persists a decision already made by the caller's policy.
func (s *State) ApplyWatchMutation(ctx context.Context, user int64, key core.RatingKey, m frontend.WatchMutation) error {
	switch m.Kind {
	case "resume", "complete", "clear", "offline-clear":
	default:
		return core.ErrNotAvailable
	}
	return s.Progress(frontend.WithTimelineStatus(ctx, m.ReportedState), user, key, m.Offset, m.Duration, "explicit-"+m.Kind)
}
