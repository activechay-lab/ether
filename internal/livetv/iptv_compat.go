package livetv

import (
	"context"
	"github.com/braedonsaunders/ether/internal/transcode"
	"net/http"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func relayPlaylistHasMedia(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			return true
		}
	}
	return false
}

// Waiting for readiness must not retain a complete video segment in memory.
type relayReadinessWriter struct {
	header http.Header
	status int
}

func (w *relayReadinessWriter) Header() http.Header { return w.header }
func (w *relayReadinessWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}
func (w *relayReadinessWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return len(b), nil
}

// Live IPTV uses standard TS or fragmented MP4 HLS. Keep the viewer's
// codec, HDR and quality ceilings; enhanced file-only containers do not apply.
func (s *Service) startIPTVRelay(ctx context.Context, ss *session, info core.MediaInfo) error {
	caps := ss.caps
	caps.VideoCodecs = []string{"h264"}
	if ss.caps.SupportsVideo("hevc") {
		caps.VideoCodecs = append(caps.VideoCodecs, "hevc")
	} else {
		caps.HDR10, caps.DolbyVision = false, false
	}
	caps.HLSContainers = nil
	tsOnly := false
	for _, c := range ss.caps.HLSContainers {
		switch c {
		case "mpegts":
			tsOnly = true
		case "mp4", "fmp4":
			caps.HLSContainers = append(caps.HLSContainers, c)
		}
	}
	tsOnly = tsOnly && len(caps.HLSContainers) == 0
	caps.AudioCodecs = nil
	for _, codec := range ss.caps.AudioCodecs {
		switch codec {
		case "aac", "ac3", "eac3", "mp3":
			caps.AudioCodecs = append(caps.AudioCodecs, codec)
		}
	}
	if len(caps.AudioCodecs) == 0 {
		caps.AudioCodecs = []string{"aac"}
	}
	dec := s.o.Transcoder.Decide(info, caps, core.DecideOptions{DirectStream: !ss.info.Channel.Transcode, AudioStream: -1, SubtitleStream: -1})
	if ss.steadyRefused == "" && s.screenStyle(ss.info.Channel, false) != "error" && (!dec.VideoCopy || !dec.AudioCopy) {
		s.revokeInput(ss)
		c := ss.info.Channel
		c.Transcode = true
		ss.info.Channel = c
		return s.startBroadcast(ctx, ss)
	}
	if tsOnly {
		dec.Container = "mpegts"
	}
	// Copy can publish at the next source keyframe without waiting four seconds.
	cut := segmentDuration
	if dec.VideoCopy && dec.VideoCodec == "hevc" {
		cut = 2 * time.Second
	}
	if dec.Container != transcode.ContainerFMP4 && dec.Container != transcode.ContainerMPEGTS {
		dec.Container = transcode.ContainerMPEGTS
	}
	input, err := s.relayInput(ss)
	if err != nil {
		return err
	}
	started := time.Now()
	s.sessionLog(ss).Info("live playback path selected", "path", string(dec.Kind), "video_copy", dec.VideoCopy, "audio_copy", dec.AudioCopy, "container", dec.Container, "video_codec", dec.VideoCodec, "audio_codec", dec.AudioCodec)
	relay, cancel, err := s.startOwnedHLS(ctx, ss, core.HLSRequest{SessionID: "livetv-" + ss.info.ID, InputURL: input, LiveInput: true, Info: info, Decision: dec, SegmentDuration: cut})
	if err != nil {
		return Diagnose("relay_start", err)
	}
	s.sessionLog(ss).Debug("live relay started", "elapsed", time.Since(started))
	ss.relay = relay
	ss.relayCancel = cancel
	output := liveRelayInfo(info, dec)
	ss.relayInfo = &output
	return nil
}

// Tune/MDE describe what the viewer receives. Keep the original probe separately
// in ss.info for any subsequent source-based decision (including DASH).
func liveRelayInfo(source core.MediaInfo, dec core.Decision) core.MediaInfo {
	out := core.MediaInfo{Container: dec.Container, BitrateKbps: dec.BitrateKbps}
	if v := source.Video(); v != nil {
		video := *v
		video.Index = 0
		if !dec.VideoCopy {
			video.Codec = dec.VideoCodec
			video.CodecString, video.Profile, video.Level = "", "", 0
			video.DolbyVision = false
			video.DolbyVisionProfile, video.DolbyVisionLevel, video.DolbyVisionCompatibilityID = 0, 0, 0
			video.BitrateKbps = 0
			if dec.MaxHeight > 0 && video.Height > dec.MaxHeight {
				video.Width = video.Width * dec.MaxHeight / video.Height
				video.Width -= video.Width % 2
				video.Height = dec.MaxHeight
			}
			if video.Codec == "h264" || dec.ToneMap {
				video.BitDepth = 8
				video.ColorTransfer = "bt709"
			}
		}
		out.Streams = append(out.Streams, video)
	}
	for _, stream := range source.Streams {
		if stream.Type != "audio" || stream.Index != dec.AudioStream {
			continue
		}
		stream.Index = len(out.Streams)
		stream.Default = true
		if !dec.AudioCopy {
			stream.Codec = dec.AudioCodec
			stream.CodecString, stream.Profile, stream.ChannelLayout = "", "", ""
			stream.Level, stream.BitrateKbps = 0, 0
			stream.SampleRate = 48000 // matches the live audio encoder
			if stream.Channels <= 0 {
				stream.Channels = 2
			}
			limit := 2
			if stream.Codec == "ac3" || stream.Codec == "eac3" {
				limit = 6
			}
			stream.Channels = min(stream.Channels, limit)
		}
		out.Streams = append(out.Streams, stream)
		break
	}
	return out
}
