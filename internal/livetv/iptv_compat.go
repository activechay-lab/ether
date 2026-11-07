package livetv

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// relayPlan decides a relay's output for the viewer: standard TS or
// fragmented MP4 HLS, keeping the viewer's codec, HDR and quality ceilings
// (enhanced file-only containers do not apply). steady reports that the
// source needs conversion and the channel's screens ask for Steady.
func (s *Service) relayPlan(ss *session, info core.MediaInfo) (dec core.Decision, cut time.Duration, steady bool) {
	caps := relayCaps(ss.caps)
	dec = s.o.Transcoder.Decide(info, caps, core.DecideOptions{DirectStream: !ss.info.Channel.Transcode, AudioStream: -1, SubtitleStream: -1})
	steady = ss.steadyRefused == "" && s.screenStyle(ss.info.Channel, false) != "error" && (!dec.VideoCopy || !dec.AudioCopy)
	if relayTSOnly(ss.caps) {
		dec.Container = "mpegts"
	}
	// Two-second segments: the first owned playlist carries two of them
	// (about four seconds of media, so a player does not start at a stalling
	// live edge) within the cold-tune budget. Copy cuts at the first source
	// keyframe after it.
	cut = relaySegment
	if dec.Container != transcode.ContainerFMP4 && dec.Container != transcode.ContainerMPEGTS {
		dec.Container = transcode.ContainerMPEGTS
	}
	return dec, cut, steady
}

const relaySegment = 2 * time.Second

func relayCaps(viewer core.ClientCaps) core.ClientCaps {
	caps := viewer
	caps.VideoCodecs = []string{"h264"}
	if viewer.SupportsVideo("hevc") {
		caps.VideoCodecs = append(caps.VideoCodecs, "hevc")
	} else {
		caps.HDR10, caps.DolbyVision = false, false
	}
	caps.HLSContainers = nil
	for _, c := range viewer.HLSContainers {
		switch c {
		case "mp4", "fmp4":
			caps.HLSContainers = append(caps.HLSContainers, c)
		}
	}
	caps.AudioCodecs = nil
	for _, codec := range viewer.AudioCodecs {
		switch codec {
		case "aac", "ac3", "eac3", "mp3":
			caps.AudioCodecs = append(caps.AudioCodecs, codec)
		}
	}
	if len(caps.AudioCodecs) == 0 {
		caps.AudioCodecs = []string{"aac"}
	}
	return caps
}

func relayTSOnly(viewer core.ClientCaps) bool {
	ts, fmp4 := false, false
	for _, c := range viewer.HLSContainers {
		switch c {
		case "mpegts":
			ts = true
		case "mp4", "fmp4":
			fmp4 = true
		}
	}
	return ts && !fmp4
}

func (s *Service) startIPTVRelay(ctx context.Context, ss *session, info core.MediaInfo) error {
	dec, cut, steady := s.relayPlan(ss, info)
	if steady {
		s.revokeInput(ss)
		c := ss.info.Channel
		c.Transcode = true
		ss.info.Channel = c
		return s.startBroadcast(ctx, ss)
	}
	if err := s.reserveRelayGrant(ss, info); err != nil {
		return err
	}
	started := time.Now()
	s.sessionLog(ss).Info("live playback path selected", "path", string(dec.Kind), "video_copy", dec.VideoCopy, "audio_copy", dec.AudioCopy, "container", dec.Container, "video_codec", dec.VideoCodec, "audio_codec", dec.AudioCodec)
	lease, err := s.startOwnedRelay(ctx, ss, info, relayCaps(ss.caps), dec, cut)
	if err != nil {
		return Diagnose("relay_start", err)
	}
	s.sessionLog(ss).Debug("live relay started", "elapsed", time.Since(started))
	ss.owned = lease
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
