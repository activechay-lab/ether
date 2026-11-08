package plex

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
	"github.com/braedonsaunders/ether/internal/sessions"
	"github.com/braedonsaunders/ether/internal/transcode"
)

const (
	stallTextDV    = "This version couldn't start in Dolby Vision on this device. Play it again to watch it in HDR10, or choose another version, such as 1080p."
	stallTextOther = "This version can't play on this device. Choose another version, such as 1080p."
)

// stallSession is the live transcode session the guard reads.
type stallSession interface {
	core.PlaybackPosition
	Decision() core.Decision
	Info() core.MediaInfo
}

// guardedStart reports whether stalled-start protection covers a session:
// Dolby Vision sources on any client, and Plex for iOS on any source. Other
// clients and sources keep their behaviour unchanged.
func guardedStart(pr *Request, v *core.StreamInfo) bool {
	if v != nil && v.DolbyVision {
		return true
	}
	return pr != nil && transcode.HasQuirk(pr.Product, pr.Platform, pr.Device, pr.Model, pr.Version, "plex-ios-hdr10-base")
}

// checkStartStall feeds one timeline to the guard. It returns the
// termination text when this client's start stalled, after remembering a
// Dolby Vision HDR10-base fallback for the next start.
func (s *Server) checkStartStall(pr *Request, key core.RatingKey, state string, offset time.Duration, tids []string) string {
	if s.d.Transcoder == nil || pr == nil || pr.User == nil {
		return ""
	}
	now := s.now()
	for _, id := range tids {
		e, ok := s.transcodes.get(id)
		if !ok || !e.started {
			continue
		}
		out, ok := s.plexHLSSession(id)
		if !ok {
			s.starts.Forget(id)
			continue
		}
		live, ok := out.(stallSession)
		if !ok {
			observer, supported := out.(interface {
				Observe(context.Context) (*remotetranscode.ObservedSession, error)
			})
			if !supported {
				continue
			}
			ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
			sample, err := observer.Observe(ctx)
			cancel()
			// No cached/zero fallback on errors, old samples or a different execution.
			if err != nil || sample == nil || sample.Identity().Session != id || sample.Identity().Generation == 0 || sample.RequestStarted().IsZero() || time.Since(sample.RequestStarted()) > 2*time.Second || time.Since(sample.RequestStarted()) < 0 {
				continue
			}
			live = sample
		}
		info := live.Info()
		v := info.Video()
		if !guardedStart(pr, v) {
			continue
		}
		base := max(e.offset, offset)
		if !s.starts.Observe(id, state, offset, live.RequestedPosition()-base, now) {
			continue
		}
		d := live.Decision()
		s.log.Warn("playback start stalled: segments delivered but the player never advanced",
			"key", key, "client", pr.ClientID, "product", pr.Product, "platform", pr.Platform, "session", id,
			"position", offset, "requested", live.RequestedPosition(), "videoCopy", d.VideoCopy, "stripDolbyVision", d.StripDolbyVision)
		if v != nil && d.VideoCopy && v.DolbyVision && !d.StripDolbyVision && transcode.HasHDR10Base(v, e.caps) {
			s.starts.Remember(sessions.StartHintKey{UserID: pr.User.ID, ClientID: pr.ClientID, Key: key}, now)
			return stallTextDV
		}
		return stallTextOther
	}
	return ""
}

// dvBaseOnly reports a remembered Dolby Vision stall for this start.
func (s *Server) dvBaseOnly(pr *Request, key core.RatingKey) bool {
	return pr != nil && pr.User != nil && s.starts.BaseOnly(sessions.StartHintKey{UserID: pr.User.ID, ClientID: pr.ClientID, Key: key}, s.now())
}
