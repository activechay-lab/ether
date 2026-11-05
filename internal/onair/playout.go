package onair

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/onair/raster"
	"github.com/braedonsaunders/ether/internal/onair/scene"
	"path/filepath"
)

// Overlay prepares an immutable scene and decoded resources for the playout
// background planner. It never renders; frame ticks evaluate the pure renderer.
func (s *Service) Overlay(ctx context.Context, r core.ClipRequest) (scene.Document, raster.Bindings, bool) {
	j, ok := s.plan(r)
	if !ok {
		return scene.Document{}, raster.Bindings{}, false
	}
	if e := s.loadAssets(ctx, &j); e != nil {
		return scene.Document{}, raster.Bindings{}, false
	}
	return j.Doc, j.Bind, true
}

// LiveStatic reports readiness without requiring a rendered clip for a scene
// whose analogue background is supplied continuously by the Signal engine.
func (s *Service) LiveStatic(ctx context.Context, r core.ClipRequest) bool {
	j, ok := s.plan(r)
	if !ok {
		return false
	}
	_, live := j.Doc.StaticForeground()
	return live
}

// ReelSnapshot reads the durable owner clip ordering and conversion states.
func (s *Service) ReelSnapshot(ctx context.Context, id string) (Reel, error) { return s.Reel(ctx, id) }

// ReelClipPath reads clip metadata and the local master index. A miss is materialized by
// the background queue, and the caller skips it until a later planning pass.
func (s *Service) ReelClipPath(ctx context.Context, reelID, clipID string) (string, bool) {
	r, e := s.Reel(ctx, reelID)
	if e != nil {
		return "", false
	}
	for _, c := range r.Clips {
		if c.ID != clipID || c.Status != "ready" {
			continue
		}
		s.mu.Lock()
		ready := s.reelReady[c.ID]
		s.mu.Unlock()
		if ready {
			return filepath.Join(s.o.Dir, "reel-"+c.ID+".mp4"), true
		}
		s.enqueue(renderJob{Hash: "reel-" + c.ID, convert: func(ctx context.Context) error { return s.convertClip(ctx, reelID, c) }})
		break
	}
	return "", false
}
