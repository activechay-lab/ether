package core

import "context"

// KindInput is a non-catalogue playback input: a plugin kind leaf whose bytes
// arrive as a random-access MediaSource rather than through the release
// pipeline. It exists so plugin video/audio items run through the same
// transcode, decision, HLS/DASH and stream-limit machinery as catalogue
// titles (spec §15.1).
type KindInput struct {
	// Title is the projected kind item. Its Key must be a plugin key
	// (RatingKey.IsPluginKey); its Type selects the profile media scope.
	Title *Title
	// Open returns the item's byte stream. It is called once per open, inside
	// the prepare flight, so stream-limit accounting and cancellation apply.
	Open func(ctx context.Context) (MediaSource, error)
	// SourceID is a stable identity for the underlying source, used as the
	// synthetic release id so session accounting and snapshots have a name.
	SourceID string
}

// KindPlayback opens a non-catalogue (plugin kind) item through the session
// and decision pipeline. It mirrors Playback.Prepare but takes a KindInput
// instead of resolving a catalogue title.
type KindPlayback interface {
	PrepareKind(ctx context.Context, user *User, in KindInput, caps ClientCaps) (*PlaySession, error)
	// SessionForKind finds the live kind session for a viewer, client and item.
	SessionForKind(userID int64, clientID string, key RatingKey) (*PlaySession, bool)
}
