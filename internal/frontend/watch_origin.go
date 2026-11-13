package frontend

import (
	"context"
	"time"
)

// WatchOrigin identifies the authenticated surface and client responsible for
// a watch update. HistoryTitle retains display identity after catalogue removal.
type WatchOrigin struct {
	// SuppressMetadataRefresh marks explicit writes that already update client state.
	SuppressMetadataRefresh bool
	HistoryTitle            *Title
	Surface                 Protocol
	SessionID, ClientID     string
	DeviceID                int64
}
type watchOriginKey struct{}

func WithWatchOrigin(ctx context.Context, origin WatchOrigin) context.Context {
	return context.WithValue(ctx, watchOriginKey{}, origin)
}
func WatchOriginFrom(ctx context.Context) WatchOrigin {
	origin, _ := ctx.Value(watchOriginKey{}).(WatchOrigin)
	return origin
}
func WithHistoryTitle(ctx context.Context, title *Title) context.Context {
	if title == nil {
		return ctx
	}
	snapshot := *title
	origin := WatchOriginFrom(ctx)
	origin.HistoryTitle = &snapshot
	return WithWatchOrigin(ctx, origin)
}

// TimelinePersistenceResult describes committed state, rather than echoing a
// client's playhead.
type TimelinePersistenceResult struct {
	PlaybackState      string
	ViewOffset         time.Duration
	ViewCount          int
	Changed, Completed bool
}
type TimelineWriter interface {
	StoreTimeline(context.Context, int64, RatingKey, time.Duration, time.Duration, string, string, *int) (TimelinePersistenceResult, error)
}

// ParentToucher updates parent season/show timestamps and completion counters
// when an episode timeline commit changes state.
type ParentToucher interface {
	TouchParents(context.Context, int64, RatingKey, time.Time, bool) error
}

// ContinueRestorer clears a continue-watching dismissal when playback resumes.
type ContinueRestorer interface {
	RestoreContinue(context.Context, int64, RatingKey) error
}

// TreeScrobbler marks a title and its descendants watched or unwatched while
// keeping parent counters independent of leaf totals.
type TreeScrobbler interface {
	ScrobbleTree(context.Context, int64, RatingKey, []RatingKey, bool) error
}

// ExactRater persists a rating while distinguishing clearing (-1) from zero.
type ExactRater interface {
	RateExact(context.Context, int64, RatingKey, float64) error
}

// LegacyProgressWriter persists a standalone playhead progress write without
// full timeline session semantics.
type LegacyProgressWriter interface {
	LegacyProgress(context.Context, int64, RatingKey, time.Duration) error
}

// ContinueHider dismisses a title or show from continue-watching.
type ContinueHider interface {
	HideContinue(context.Context, int64, RatingKey) error
}

// CompletedShowsState reports which shows have had an episode completed in playback.
type CompletedShowsState interface {
	PlaybackCompletedShows(context.Context, int64, []RatingKey) (map[RatingKey]bool, error)
}

// PendingWatchState reports whether a viewer has uncommitted watch updates in flight.
type PendingWatchState interface {
	HasPendingWatch(int64) bool
}

// RecentlyViewedSettingsState reads history filtered by recently-viewed settings.
type RecentlyViewedSettingsState interface {
	RecentlyViewedSettings(context.Context, int64, time.Time, int) ([]HistoryEntry, error)
}

type timelineStoredKey struct{}

func WithTimelineStored(ctx context.Context) context.Context {
	return context.WithValue(ctx, timelineStoredKey{}, true)
}
func TimelineStored(ctx context.Context) bool {
	stored, _ := ctx.Value(timelineStoredKey{}).(bool)
	return stored
}
