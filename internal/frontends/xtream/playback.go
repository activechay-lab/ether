package xtream

import (
	"errors"
	"net/http"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/caps"
)

func (i *instance) active(r *http.Request, l login) int {
	if status, ok := i.host.LazyStreams.(frontend.AppConnectionStatus); ok {
		n, err := status.AppConnections(r.Context(), l.password)
		if err == nil {
			return n
		}
	}
	ss, err := i.host.Sessions.Sessions(r.Context(), frontend.SessionAccess{Viewer: l.viewer, Device: l.device, Protocol: "xtream"})
	if err != nil {
		return 0
	}
	n := 0
	for _, s := range ss {
		if s.Device.ID == l.device.ID {
			n++
		}
	}
	return min(n, 1)
}
func (i *instance) play(w http.ResponseWriter, r *http.Request, l login) {
	if i.host.Capabilities == nil || i.host.LazyStreams == nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	file := r.PathValue("file")
	id, ext, ok := strings.Cut(file, ".")
	if file != r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:] || !ok || (ext != "ts" && ext != "m3u8" && ext != "mkv" && ext != "mp4") {
		http.NotFound(w, r)
		return
	}
	ip, ok := i.host.ClientIP(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	ctx := frontend.WithClientIP(r.Context(), ip)
	profile, err := i.host.Capabilities.Build(ctx, frontend.CapabilityRequest{Viewer: l.viewer, Device: caps.DeviceInfo{Protocol: "xtream", ClientID: l.device.ID, Product: l.device.Product, VideoCodecs: []string{"h264", "hevc", "mpeg2video", "av1", "vp9"}, AudioCodecs: []string{"aac", "ac3", "eac3", "dts", "truehd", "flac", "mp3", "opus"}, Containers: []string{"mkv", "mp4", "mpegts", "ts"}, MaxHeight: 4320, HDR10: true}, UserAgent: r.UserAgent()})
	if err != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	no := false
	req := frontend.PlanRequest{Viewer: l.viewer, Device: l.device, Protocol: "xtream", Caps: profile.Client, Intent: frontend.PlayIntent{AllowTranscoding: &no, Options: frontend.DecideOptions{DirectPlay: true, DirectStream: true}}}
	kind := strings.ToLower(strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0])
	if kind == "live" {
		c, ok := i.findChannel(r, l, id)
		if !ok {
			http.NotFound(w, r)
			return
		}
		req.Channel = c.ID
		req.Intent.AllowTranscoding = nil // Shared live output is already encoded by the channel service.
		if !i.admit(w, r.WithContext(ctx), l, req) {
			return
		}
		path, err := i.host.LazyStreams.Issue(ctx, l.password, req)
		if err != nil || !strings.HasPrefix(path, "/sessions/lazy/") {
			http.Error(w, "Unavailable", 503)
			return
		}
		base := i.base(r)
		if base == "" {
			http.Error(w, "Unavailable", 503)
			return
		}
		http.Redirect(w, r, base+path, http.StatusTemporaryRedirect)
		return
	}
	key, err := i.resolveItem(r, id)
	if err != nil || key <= 0 {
		http.NotFound(w, r)
		return
	}
	detail, err := i.host.Experience.Item(ctx, frontend.ItemRequest{Viewer: l.viewer, Device: l.device, Key: frontend.RatingKey(key)})
	if err != nil || kind == "movie" && detail.Title.Type != "movie" || kind == "series" && detail.Title.Type != "episode" {
		http.NotFound(w, r)
		return
	}
	req.Item = detail.Title.Key
	if !i.admit(w, r.WithContext(ctx), l, req) {
		return
	}
	lazy, ok := i.host.LazyStreams.(frontend.AuthenticatedLazyStreams)
	if !ok {
		http.Error(w, "Unavailable", 503)
		return
	}
	// The normal broker serves the original file and implements Range/If-Range.
	lazy.ServeAuthenticated(w, r.WithContext(ctx), l.password, req)
}

func (i *instance) admit(w http.ResponseWriter, r *http.Request, l login, req frontend.PlanRequest) bool {
	if admission, ok := i.host.LazyStreams.(frontend.AppConnectionAdmission); ok {
		if err := admission.PrepareAppConnection(r, l.password, req); err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, frontend.ErrStreamLimit) {
				status = http.StatusTooManyRequests
			}
			if errors.Is(err, frontend.ErrUnauthorized) {
				status = http.StatusUnauthorized
			}
			http.Error(w, "Playback unavailable", status)
			return false
		}
	}
	return true
}
