package core

import (
	"context"
	"time"
)

// ClipRequest binds a look moment to the channel and upcoming programme.
type ClipRequest struct {
	LookID    string
	Moment    string
	ChannelID string
	ViewerID  int64
	Bind      map[string]string
	Art       map[string]string
	Language  string
}
type ClipRef struct {
	Hash     string
	Duration time.Duration
	Path     string
}

// ChannelGraphics never renders synchronously. Clip only reads the local cache.
type ChannelGraphics interface {
	Duration(lookID, moment string) (time.Duration, bool)
	Clip(context.Context, ClipRequest) (ClipRef, bool)
	Ensure(context.Context, ClipRequest)
}
