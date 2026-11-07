package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// Steady is the IPTV default: decode, normalise to one format, re-encode on
// a continuous timeline. Three things send a tune to the direct paths
// instead: a 4K or HDR source (Steady normalises to one SDR format, which
// would be a visible compromise), and a host without encode capacity left
// for another channel. A running channel is never degraded to admit another.

// steadyShape is the working format and output quality of one Steady channel.
type steadyShape struct {
	format      transcode.RawFormat
	bitrateKbps int
}

// Software cost of one Steady channel in cores, relative to 1080p30 (decode,
// deinterlace/scale, raw transfer and x264 encode), measured on motion
// content. A hardware encoder leaves decode and scaling on the CPU.
const (
	steadySoftwareCores1080p30 = 2.0
	steadyHardwareCores1080p30 = 0.45
	// Keep this share of the host for VOD, guide work and the server itself.
	steadyHostShare = 0.6
)

func steadyCost(f transcode.RawFormat, hardware bool) float64 {
	if f.FPSNum <= 0 || f.FPSDen <= 0 {
		return 0
	}
	scale := float64(f.Width*f.Height) * float64(f.FPSNum) / float64(f.FPSDen) / (1920 * 1080 * 30)
	if hardware {
		return steadyHardwareCores1080p30 * scale
	}
	return steadySoftwareCores1080p30 * scale
}

// steadyRate snaps a probed frame rate onto a broadcast rate. Interlaced
// sources are deinterlaced field by field, so 1080i50 airs as 50p.
func steadyRate(v *core.StreamInfo) (int, int) {
	fps := 0.0
	if v != nil {
		fps = v.FrameRate
		if v.AverageFrameRate != nil && *v.AverageFrameRate > 0 && (fps <= 0 || fps > 120) {
			fps = *v.AverageFrameRate
		}
		if v.Interlaced && fps > 0 && fps <= 30.5 {
			fps *= 2
		}
	}
	if fps <= 0 || math.IsNaN(fps) {
		return 30, 1
	}
	rates := [][2]int{{24000, 1001}, {24, 1}, {25, 1}, {30000, 1001}, {30, 1}, {50, 1}, {60000, 1001}, {60, 1}}
	best, diff := rates[4], math.Inf(1)
	for _, r := range rates {
		if d := math.Abs(float64(r[0])/float64(r[1]) - fps); d < diff {
			best, diff = r, d
		}
	}
	return best[0], best[1]
}

// steadyShapeFor follows the source: its frame rate, its resolution up to
// 1080p, and a bitrate ceiling at or above its own. Rate control stays
// quality-targeted under that ceiling; there is no server-side cap.
func steadyShapeFor(info *core.MediaInfo) steadyShape {
	var v *core.StreamInfo
	if info != nil {
		v = info.Video()
	}
	num, den := steadyRate(v)
	height := 1080
	if v != nil && v.Height > 0 {
		switch {
		case v.Height >= 1000:
			height = 1080
		case v.Height >= 700:
			height = 720
		default:
			height = 576
		}
	}
	width := height * 16 / 9
	width -= width % 2
	fast := float64(num)/float64(den) > 31
	floor := map[int][2]int{1080: {8000, 12000}, 720: {5000, 7500}, 576: {3000, 4500}}[height]
	bitrate := floor[0]
	if fast {
		bitrate = floor[1]
	}
	source := 0
	if v != nil && v.BitrateKbps > 0 {
		source = v.BitrateKbps
	} else if info != nil && info.BitrateKbps > 0 {
		source = info.BitrateKbps
	}
	if source > 0 {
		bitrate = max(bitrate, min(25000, source*5/4))
	}
	return steadyShape{format: transcode.RawFormat{Width: width, Height: height, FPSNum: num, FPSDen: den, SampleRate: 48000, Channels: 2, PixelFormat: "yuv420p"}, bitrateKbps: bitrate}
}

// steadyRefusal says why a probed source must not use Steady, or "".
func steadyRefusal(info *core.MediaInfo) string {
	if info == nil {
		return ""
	}
	v := info.Video()
	if v == nil {
		return ""
	}
	audio := false
	for _, st := range info.Streams {
		audio = audio || st.Type == "audio"
	}
	if !audio {
		// The continuous decoder pairs every picture with its sound.
		return "source without audio plays direct"
	}
	if v.Height > 1080 || v.Width > 1920 {
		return "source above 1080p stays direct"
	}
	if v.DolbyVision || v.ColorTransfer == "smpte2084" || v.ColorTransfer == "arib-std-b67" {
		return "HDR source stays direct"
	}
	return ""
}

// steadyLoad is the estimated CPU of every running continuous output.
func (r *broadcastRegistry) steadyLoad(hardware bool) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	load := 0.0
	for _, b := range r.outputs {
		load += steadyCost(b.format, hardware)
	}
	return load
}

func (r *broadcastRegistry) running(key broadcastKey) *broadcast {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.outputs[key]
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	return b
}

func (s *Service) steadyHardware() bool {
	if s.o.Transcoder == nil {
		return false
	}
	hw := s.o.Transcoder.HWAccel()
	return hw.Name != "" && hw.Name != "none"
}

// continuousBudget is a transcoder that can size continuous outputs.
type continuousBudget interface {
	ContinuousBudget() (cores float64, hardware bool)
}

// errSteadyRefused reports a tune Steady declined; the caller takes a direct
// path. The session's steadyRefused holds the reason.
var errSteadyRefused = errors.New("live TV: continuous output declined")

// steadyCapacity reports whether one more output of format f fits the host.
// A transcoder that cannot size continuous outputs runs none for IPTV.
func (s *Service) steadyCapacity(f transcode.RawFormat) (bool, float64, float64) {
	source, ok := s.o.Transcoder.(continuousBudget)
	if !ok {
		return false, 0, 0
	}
	budget, hardware := source.ContinuousBudget()
	load := s.broadcasts.steadyLoad(hardware)
	return load+steadyCost(f, hardware) <= budget, load, budget
}

// steadyStrainLag is the publication lag at which running outputs no longer
// leave real-time headroom for another: a third of an Apple player's 3 s
// stale-playlist budget at 2 s segments.
const steadyStrainLag = time.Second

// steadyStrained reports a running continuous output that is falling behind
// real time: its encoder queue is repeating pictures, or a segment was
// published late within the last minute. A new channel would only make every
// running one worse, so it plays direct instead.
func (r *broadcastRegistry) steadyStrained() bool {
	r.mu.Lock()
	outputs := make([]*broadcast, 0, len(r.outputs))
	for _, b := range r.outputs {
		outputs = append(outputs, b)
	}
	r.mu.Unlock()
	for _, b := range outputs {
		b.mu.Lock()
		for _, rendition := range b.renditions {
			rendition.queue.mu.Lock()
			repeating := rendition.queue.repeating
			rendition.queue.mu.Unlock()
			lagging := false
			if lag, ok := rendition.encoder.(interface{ RecentPublishLag() time.Duration }); ok {
				lagging = lag.RecentPublishLag() > steadyStrainLag
			}
			if repeating || lagging {
				b.mu.Unlock()
				return true
			}
		}
		b.mu.Unlock()
	}
	return false
}

// steadyAdmission is a probed first input for a new Steady output.
type steadyAdmission struct {
	shape steadyShape
	link  *steadyLink
	info  *core.MediaInfo
}

// admitSteady probes the best input of channel c through a relay grant that
// the output's first decoder then reuses, so the decoder starts from the
// bytes the probe already read on the same provider connection. It returns
// a refusal reason when the tune must take a direct path.
func (s *Service) admitSteady(ctx context.Context, c Channel) (*steadyAdmission, string) {
	if _, ok := s.o.Transcoder.(continuousBudget); !ok {
		return nil, "transcoder runs no continuous outputs"
	}
	if s.broadcasts.steadyStrained() {
		return nil, "running channels are falling behind real time"
	}
	rockSolid := s.stabilityPreset(c) == stabilityRockSolid
	// Refuse before touching the provider when not even the smallest
	// broadcast shape would fit.
	smallest := transcode.RawFormat{Width: 1024, Height: 576, FPSNum: 25, FPSDen: 1}
	if ok, load, budget := s.steadyCapacity(smallest); !ok && !rockSolid {
		return nil, "encode capacity in use (" + formatCores(load) + " of " + formatCores(budget) + " cores)"
	}
	var probed *steadyAdmission
	candidates := s.orderedSourceCandidates(c)
	for i, src := range candidates {
		if ctx.Err() != nil {
			break
		}
		link, err := s.linkSteadySource(ctx, c, src, "broadcast-"+c.ID, 2*steadySegment)
		if errors.Is(err, ErrProviderBusy) || errors.Is(err, ErrProviderPaced) {
			continue
		}
		if err != nil {
			s.recordSourceHealth(c.ID, src, err, false)
			continue
		}
		// A hanging input leaves the tune's budget for the next one.
		probeBudget := 6 * time.Second
		if i < len(candidates)-1 {
			probeBudget = 3 * time.Second
		}
		attempt, cancel := context.WithTimeout(ctx, probeBudget)
		info, err := s.probeSessionInput(attempt, link.ss)
		cancel()
		if err == nil && info != nil && info.Video() == nil && len(info.Streams) > 0 {
			// Radio: Steady renders pictures; an audio-only source plays direct.
			link.close(s)
			return nil, "audio-only source plays direct"
		}
		if err != nil || info == nil || info.Video() == nil {
			if err == nil {
				err = core.ErrNotAvailable
			}
			s.recordSourceHealth(c.ID, src, err, false)
			link.close(s)
			continue
		}
		link.format = steadyInputFormat(info)
		probed = &steadyAdmission{shape: steadyShapeFor(info), link: link, info: info}
		break
	}
	if probed == nil {
		// Nothing answered within the tune: Steady still starts on its
		// reconnecting screen and its ladder keeps trying every input.
		probed = &steadyAdmission{shape: steadyShapeFor(nil)}
	}
	if reason := steadyRefusal(probed.info); reason != "" {
		probed.link.close(s)
		return nil, reason
	}
	if !rockSolid {
		if ok, load, budget := s.steadyCapacity(probed.shape.format); !ok {
			probed.link.close(s)
			return nil, "encode capacity in use (" + formatCores(load) + " of " + formatCores(budget) + " cores)"
		}
	}
	return probed, ""
}

func formatCores(v float64) string {
	b, _ := json.Marshal(math.Round(v*10) / 10)
	return string(b)
}

// steadyInputFormat names the demuxer of a probed live input.
func steadyInputFormat(info *core.MediaInfo) string {
	if info == nil {
		return ""
	}
	switch info.Container {
	case "hls", "mpegts":
		return info.Container
	}
	return ""
}

// cachedInputFormat reads the demuxer of a granted input from the probe
// cache without probing; a recovery never waits for ffprobe.
func (s *Service) cachedInputFormat(ss *session) string {
	key := ss.inputURL
	if len(ss.inputHeaders) != 0 {
		encoded, _ := json.Marshal(ss.inputHeaders)
		key = stableID(ss.inputURL, string(encoded))
	}
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	if entry, ok := s.probeCache[key]; ok {
		return steadyInputFormat(&entry.info)
	}
	return ""
}
