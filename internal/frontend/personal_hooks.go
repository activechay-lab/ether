package frontend

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// PostPlayPick is one ranked post-play recommendation with its explained
// score components. The ranking stays owned by core.
type PostPlayPick = core.PostPlayPick

// MicroReason is the per-viewer decision header for a detail page. Its
// construction stays owned by core.
type MicroReason = core.MicroReason

// PostPlayer ranks a personalized post-play queue after a title.
type PostPlayer interface {
	PostPlay(ctx context.Context, user *User, justWatched RatingKey, hc HubContext, limit int) ([]PostPlayPick, error)
}

// MicroReasoner builds a MicroReason for one viewer and title. The title
// is only read, never mutated.
type MicroReasoner interface {
	MicroReason(ctx context.Context, user *User, t *Title, now time.Time) MicroReason
}

// IntentTracker receives live in-session browsing signals: detail opens
// that do not turn into playback.
type IntentTracker interface {
	RecordDetailOpen(ctx context.Context, user *User, t *Title)
	NotePlayback(user *User, key RatingKey)
}

// VibeSearcher builds ephemeral collections from a natural-language query
// and promotes them to persisted personal smart collections.
type VibeSearcher interface {
	VibeCollection(ctx context.Context, user *User, query string) (c *Collection, items []Title, ok bool, err error)
	PromoteEphemeral(ctx context.Context, user *User, key RatingKey) (c *Collection, ok bool, err error)
}
