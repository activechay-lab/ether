package livetv

import (
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// offAirCopy is the single home of generated viewer copy. Look ahead only on
// this channel, one day at a time, through a complete weekly/repeat calendar.
func offAirCopy(c Channel, p Programme) []string {
	lines := []string{offAirTitle, c.Name}
	if len(c.Items) == 0 && len(c.Fallback) == 0 && len(c.Blocks) == 0 {
		return lines
	}
	days := max(8, c.RepeatDays+8)
	for _, b := range c.Blocks {
		days = max(days, b.RepeatDays+8)
	}
	for day := 0; day < days; day++ {
		from := p.End.AddDate(0, 0, day)
		for _, next := range Schedule(c, from, from.AddDate(0, 0, 1)) {
			if next.Item.Slate == offAirSlate || next.Start.Before(p.End) {
				continue
			}
			loc := time.UTC
			if loaded, err := time.LoadLocation(c.Timezone); err == nil {
				loc = loaded
			}
			return append(lines, "Back at "+next.Start.In(loc).Format("3:04 PM"))
		}
	}
	return lines
}

// slateInfo follows the preceding negotiated output when available. A tune
// into an empty channel has a conventional SDR H.264/AAC stereo contract.
func (s *Service) slateInfo(previous *core.MediaInfo, caps core.ClientCaps, duration time.Duration) core.MediaInfo {
	v := core.StreamInfo{Index: 0, Type: "video", Codec: "h264", Width: 1280, Height: 720, FrameRate: 25, Profile: "High", PixelFormat: "yuv420p"}
	a := core.StreamInfo{Index: 1, Type: "audio", Codec: "aac", Channels: 2, SampleRate: 48000}
	if previous != nil {
		decision := s.o.Transcoder.Decide(*previous, caps, core.DecideOptions{DirectStream: true, AudioStream: -1, SubtitleStream: -1})
		if old := previous.Video(); old != nil {
			v.Width, v.Height = old.Width, old.Height
			if decision.VideoCopy || decision.Kind == core.DecisionDirectPlay {
				v.Codec = old.Codec
				v.Profile = old.Profile
			} else if decision.VideoCodec != "" {
				v.Codec = decision.VideoCodec
			}
			if decision.MaxHeight > 0 && v.Height > decision.MaxHeight {
				v.Width = v.Width * decision.MaxHeight / v.Height
				v.Height = decision.MaxHeight
			}
		}
		for _, old := range previous.Streams {
			if old.Type == "audio" {
				a.Channels, a.SampleRate = old.Channels, old.SampleRate
				a.ChannelLayout = old.ChannelLayout
				if decision.AudioCopy {
					a.Codec = old.Codec
				}
				break
			}
		}
		if decision.AudioChannels > 0 {
			a.Channels = decision.AudioChannels
		}
		if decision.AudioSampleRate > 0 {
			a.SampleRate = decision.AudioSampleRate
		}
	}
	switch v.Codec {
	case "h264", "hevc", "av1", "vp9":
	default:
		v.Codec = "h264"
	}
	switch a.Codec {
	case "aac", "ac3", "eac3", "mp3", "flac", "opus":
	default:
		a.Codec = "aac"
	}
	if a.Channels < 1 || a.Channels > 8 {
		a.Channels = 2
	}
	if a.SampleRate <= 0 {
		a.SampleRate = 48000
	}
	v.Width = max(2, v.Width/2*2)
	v.Height = max(2, v.Height/2*2)
	return core.MediaInfo{Container: "mp4", Duration: duration, Streams: []core.StreamInfo{v, a}}
}
