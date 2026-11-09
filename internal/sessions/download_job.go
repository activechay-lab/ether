package sessions

import (
	"context"
	"strconv"

	"github.com/braedonsaunders/ether/internal/core"
)

// DownloadJob owns one offline source lease, including bounded quality retries
// and retirement on every exit. Queue IDs, status rendering and track intent
// stay with the caller. A run must be unique within that caller's queue store.
type DownloadJob struct {
	Playback core.Playback
	Remuxer  core.DownloadRemuxer
}

type DownloadRequest struct {
	Viewer    *core.User
	Item      core.RatingKey
	Caps      core.ClientCaps
	Run       int64
	NoRelease func(error) bool
	Prepared  func(*core.PlaySession) (core.DecideOptions, error)
	Progress  func(float64)
}

type DownloadPreparationError struct{ Err error }

func (e *DownloadPreparationError) Error() string { return e.Err.Error() }
func (e *DownloadPreparationError) Unwrap() error { return e.Err }

func (j DownloadJob) Run(ctx context.Context, q DownloadRequest) (string, int64, error) {
	caps := q.Caps
	caps.Download = true
	// Separate jobs must not share a source with live playback or another run.
	caps.ClientID += "/download/" + strconv.FormatInt(q.Run, 10)
	ps, err := j.Playback.Prepare(ctx, q.Viewer, q.Item, caps)
	for _, height := range []int{1080, 2160} {
		if !q.NoRelease(err) || ctx.Err() != nil || caps.MaxHeight <= 0 || height <= caps.MaxHeight {
			continue
		}
		sourceCaps := caps
		sourceCaps.DownloadSourceMaxHeight = height
		ps, err = j.Playback.Prepare(ctx, q.Viewer, q.Item, sourceCaps)
	}
	if err != nil {
		return "", 0, &DownloadPreparationError{Err: err}
	}
	defer j.Playback.Stop(ps.ID)
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	opts, err := q.Prepared(ps)
	if err != nil {
		return "", 0, err
	}
	var viewer int64
	if q.Viewer != nil {
		viewer = q.Viewer.ID
	}
	return j.Remuxer.RemuxDownload(ctx, core.DownloadRemuxRequest{
		InputURL: j.Playback.InputURL(ps.ID), Info: ps.Info, AudioStream: -1,
		ViewerID: viewer, Caps: caps, Options: opts,
	}, q.Progress)
}
