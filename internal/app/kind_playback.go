package app

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// kindAccept lists the retrieval patterns the transcode bridge can turn into
// bytes. "direct" is excluded: a redirect cannot be probed or encoded, so a
// kind source must be a real stream.
var kindAccept = []string{"range", "download", "container", "release"}

// kindSourceFunc is the broker's KindSource dep: it projects a plugin kind
// item and hands back an opener for its bytes (spec §15.1). The opener
// re-authorizes the viewer through KindResolver.OpenKind, so authorization
// is enforced at open time, not on the projection alone.
func (a *App) kindSourceFunc() func(context.Context, *core.Viewer, core.RatingKey) (*core.KindInput, error) {
	if a.pluginsRT == nil || a.pluginsRT.kindCatalog == nil || a.pluginsRT.kindResolver == nil {
		return nil
	}
	catalog := a.pluginsRT.kindCatalog
	resolver := a.pluginsRT.kindResolver
	return func(ctx context.Context, viewer *core.Viewer, key core.RatingKey) (*core.KindInput, error) {
		if viewer == nil || !key.IsPluginKey() {
			return nil, core.ErrNotFound
		}
		piv, err := catalog.Item(ctx, key)
		if err != nil {
			return nil, core.ErrNotFound
		}
		title := piv.Title
		return &core.KindInput{
			Title:    &title,
			SourceID: key.String(),
			Open: func(octx context.Context) (core.MediaSource, error) {
				media, err := resolver.OpenKind(octx, viewer, key, kindAccept)
				if err != nil {
					return nil, err
				}
				return kindMediaSource{media: media, releaseID: "kind:" + key.String()}, nil
			},
		}, nil
	}
}

// kindMediaSource adapts a frontend.KindMedia (a byte-addressable plugin
// stream) into a core.MediaSource so the playback session pipeline can probe
// and encode it like any other source.
type kindMediaSource struct {
	media     *frontend.KindMedia
	releaseID string
}

func (k kindMediaSource) ReadAt(p []byte, off int64) (int, error) { return k.media.ReadAt(p, off) }
func (k kindMediaSource) Size() int64                             { return k.media.Size }
func (k kindMediaSource) Name() string {
	if k.media.Name != "" {
		return k.media.Name
	}
	return k.releaseID
}
func (k kindMediaSource) ReleaseID() string { return k.releaseID }
func (k kindMediaSource) Close() error      { return k.media.Close() }
