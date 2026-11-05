package app

import (
	"context"
	"github.com/braedonsaunders/ether/internal/livetv"
	"time"
)

// Saved reel metadata is copied only by Live TV's background refresh.
func (a *App) onairReelSnapshot(ctx context.Context, id string) (livetv.ReelSnapshot, error) {
	reel, err := a.onair.ReelSnapshot(ctx, id)
	if err != nil {
		return livetv.ReelSnapshot{}, err
	}
	out := livetv.ReelSnapshot{ID: reel.ID, Anchor: reel.Anchor, Order: reel.Order, Repeat: time.Duration(reel.RepeatHours * float64(time.Hour))}
	for _, clip := range reel.Clips {
		if clip.Status != "ready" || clip.DurationMS <= 0 {
			continue
		}
		path, _ := a.onair.ReelClipPath(ctx, reel.ID, clip.ID)
		out.Clips = append(out.Clips, livetv.ReelClip{ID: clip.ID, Name: clip.Name, Duration: time.Duration(clip.DurationMS) * time.Millisecond, Path: path})
	}
	return out, nil
}
