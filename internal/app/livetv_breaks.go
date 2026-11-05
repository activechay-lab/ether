package app

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/markers"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// channelBreakInput uses playback's configured source engine and loopback range
// transport. Its dedicated prepared source is retired immediately after analysis.
func (a *App) channelBreakInput(ctx context.Context, key core.RatingKey) (markers.BreakInput, error) {
	u, err := a.users.Owner(ctx)
	if err != nil {
		return markers.BreakInput{}, err
	}
	caps := core.ClientCaps{Product: "Ether act-break analysis", ClientID: "ether-channel-break-analysis", MaxHeight: 1080, VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac", "ac3", "eac3"}, Containers: []string{"mp4", "mkv"}}
	ps, retire, err := a.playback.PrepareSurfaceSource(ctx, u, key, caps, "", false)
	if err != nil {
		return markers.BreakInput{}, err
	}
	closeInput := func() { _ = a.playback.RetireSource(ps.ID, retire) }
	revision := core.SourceRevision(ps)
	if err = a.playback.PinSourceRevision(ctx, ps.ID, revision); err != nil {
		closeInput()
		return markers.BreakInput{}, err
	}
	closePinned := func() { a.playback.UnpinSourceRevision(ps.ID); closeInput() }
	info := ps.Info
	transcode.ProbeTrailer(ctx, ps.Source, ps.Source.Size(), &info)
	payload := ""
	if ps.Resolution != nil {
		payload = fmt.Sprintf("%x", sha256.Sum256(ps.Resolution.NZB))
	}
	return markers.BreakInput{ReleaseID: ps.Source.ReleaseID(), Revision: revision, ReleaseKey: fmt.Sprintf("%s:%s:%d", revision, payload, info.Duration.Milliseconds()), URL: a.playback.InputURL(ps.ID), Info: info, Close: closePinned}, nil
}
