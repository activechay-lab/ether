//go:build !scorecard

package app

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
)

func scorecardVariants(ctx context.Context, a *App, v *core.Viewer, t *core.Title, caps core.ClientCaps) ([]core.ReleaseVariant, error) {
	if vs, ok := labMediaVariants(a.opts.DataDir, t); ok {
		return vs, nil
	}
	req := core.ResolveRequest{Title: t, User: a.withEffectiveAudioLanguages(ctx, v), Client: caps}
	// After a play, Auto is named after the release that played (the one a
	// resume opens), as Plex shows it.
	if a.playback != nil && v != nil && v.ID > 0 {
		if _, played, ok := a.playback.ResumedTracks(ctx, v, t.Key, caps); ok {
			req.PlayedRelease = played
		}
	}
	variants, err := a.resolver.Variants(ctx, req, 4)
	if err == nil && a.playback != nil {
		variants = a.playback.WithCachedVariantInfo(ctx, v, t, variants)
	}
	return variants, err
}
