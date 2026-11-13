// Package timeline owns the PMS session crossing and resume policy.
package timeline

import (
	"context"

	"github.com/braedonsaunders/ether/internal/frontend"
	"sync"
	"time"
)

// Writer binds a frontend-owned session cache to shared persistence.
type Writer struct {
	frontend.State
	Store *Store
	Now   func() time.Time
}

func (w Writer) TouchParents(ctx context.Context, user int64, key frontend.RatingKey, at time.Time, complete bool) error {
	if p, ok := w.State.(frontend.ParentToucher); ok {
		return p.TouchParents(ctx, user, key, at, complete)
	}
	return nil
}
func (w Writer) RestoreContinue(ctx context.Context, user int64, key frontend.RatingKey) error {
	if p, ok := w.State.(frontend.ContinueRestorer); ok {
		return p.RestoreContinue(ctx, user, key)
	}
	return nil
}

type playID struct {
	user            int64
	client, session string
	playlist        bool
}
type timelinePlay struct {
	key                          frontend.RatingKey
	queue                        string
	position                     time.Duration
	progressed, complete, closed bool
	at                           time.Time
}
type Store struct {
	sync.Mutex
	plays map[playID]timelinePlay
}

// TimelineStore applies the session-local crossing and resume rules measured on PMS.
// The media and playlist tracks are independent. The cache has a fixed bound;
// its cost depends on active clients, never the catalogue's size.
func (s Writer) StoreTimeline(ctx context.Context, user int64, key frontend.RatingKey, offset, duration time.Duration, status, queue string, offline *int) (frontend.TimelinePersistenceResult, error) {
	s.Store.Lock()
	defer s.Store.Unlock()
	cur, err := s.Get(ctx, user, key)
	if err != nil {
		return frontend.TimelinePersistenceResult{}, err
	}
	out := frontend.TimelinePersistenceResult{PlaybackState: "ignore", ViewOffset: cur.ViewOffset, ViewCount: cur.ViewCount}
	if offline != nil {
		if *offline < 0 {
			return out, nil
		}
		mode := "offline-clear"
		out.PlaybackState = "complete"
		if *offline > 0 {
			mode = "complete"
			out.PlaybackState = "complete"
			out.Completed = true
			out.ViewCount++
		} else if cur.ViewCount == 0 {
			return out, nil
		}
		err = s.apply(ctx, user, key, offset, duration, mode, status)
		out.Changed = err == nil
		out.ViewOffset = 0
		if *offline == 0 {
			out.ViewCount = 0
		}
		return out, err
	}
	origin := frontend.WatchOriginFrom(ctx)
	id := playID{user, origin.ClientID, origin.SessionID, key.Kind() == frontend.KindPlaylist}
	if s.Store.plays == nil {
		s.Store.plays = make(map[playID]timelinePlay)
	}
	play := s.Store.plays[id]
	previous := play.position
	if play.key != key || play.queue != queue {
		play = timelinePlay{key: key, queue: queue}
	}
	now := s.Now()
	play.at = now
	mode := ""
	if duration > 0 {
		threshold := time.Duration(duration.Milliseconds()*9/10) * time.Millisecond
		nearEnd := offset >= threshold
		if !nearEnd {
			play.complete = false
			play.closed = false
			if offset > time.Minute {
				play.progressed = true
			}
		}
		crossing := nearEnd && previous > 0 && previous < threshold && !play.complete && !play.closed
		if crossing {
			play.complete = true
			play.progressed = true
			mode = "complete"
			out.Completed = true
			out.ViewCount++
			out.ViewOffset = 0
		}
		switch {
		case play.complete && !play.closed:
			out.PlaybackState = "complete"
		case play.closed:
			out.PlaybackState = "ignore"
		case play.progressed:
			out.PlaybackState = "progress"
		}
		if mode == "" && !nearEnd && offset > time.Minute && offset != cur.ViewOffset {
			mode = "resume"
			out.ViewOffset = offset
		} else if mode == "" && !nearEnd && offset <= time.Minute && play.progressed && status != "stopped" && cur.ViewOffset != 0 {
			mode = "clear"
			out.ViewOffset = 0
		}
	}
	play.position = offset
	if status == "stopped" {
		if play.complete {
			play.closed = true
		}
		play.progressed = offset > time.Minute && (play.progressed || play.complete)
	}
	// Refused requests never reach this function. Missing duration is a no-op.
	s.Store.plays[id] = play
	if len(s.Store.plays) > 4096 {
		var oldest playID
		var at time.Time
		for k, p := range s.Store.plays {
			if at.IsZero() || p.at.Before(at) {
				oldest, at = k, p.at
			}
		}
		delete(s.Store.plays, oldest)
	}
	if mode != "" {
		err = s.apply(ctx, user, key, offset, duration, mode, status)
		out.Changed = err == nil
	}
	return out, err
}

func (s Writer) apply(ctx context.Context, user int64, key frontend.RatingKey, offset, duration time.Duration, kind, status string) error {
	writer, ok := s.State.(frontend.WatchMutationWriter)
	if !ok {
		return frontend.ErrNotAvailable
	}
	return writer.ApplyWatchMutation(ctx, user, key, frontend.WatchMutation{Kind: kind, Offset: offset, Duration: duration, ReportedState: status})
}
