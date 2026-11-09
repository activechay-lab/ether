package core

import (
	"context"
	"io"
)

// PlaybackSourceResource provides a revision-bound, lifetime-canceled read
// lease over a session's currently opened source generation without requiring
// the session to have entered actual playing state.
type PlaybackSourceResource interface {
	WithSourceResource(ctx context.Context, id, revision string, fn func(ctx context.Context, src io.ReaderAt, size int64) error) error
}
