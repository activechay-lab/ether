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

// PersonalPick is a viewer-safe programming candidate with its explanation.
type PersonalPick struct {
	Title  *Title
	Reason string
	Kind   string
	Score  float64
}

// PersonalPicker supplies background personal-channel rundown builders.
type PersonalPicker interface {
	Picks(context.Context, *User, map[string]float64, int) ([]PersonalPick, error)
}
