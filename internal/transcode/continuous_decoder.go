package transcode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type continuousDecoder struct {
	*continuousProcess
	frames   chan RawFrame
	readDone chan struct{}
	readErr  error
	workers  sync.WaitGroup
	reads    [2]atomic.Int64
	ptsStop  chan struct{}
	ptsOnce  sync.Once
	// waitingSince is when the assembler began waiting for one stream (unix
	// ns, 0 while not waiting); other is the queue of the stream it is not
	// waiting for. Together they tell a starving stream from a quiet input.
	audioStart   atomic.Int64 // first audio frame's source PTS (ns), -1 until seen
	waitingSince atomic.Int64
	otherStream  atomic.Int64 // reads index of the stream not waited for
	otherAtWait  atomic.Int64 // its buffers read when the wait began
	errorLines   atomic.Int64 // decoder error lines on stderr
	errorsAtWait atomic.Int64
}

// StarvingErrors is Starving plus whether the decoders reported errors
// during the wait: a codec change floods them, interleave jitter does not.
func (d *continuousDecoder) StarvingErrors() (time.Duration, bool) {
	starved := d.Starving()
	return starved, starved > 0 && d.errorLines.Load() > d.errorsAtWait.Load()
}

// Starving reports how long one elementary stream has delivered nothing
// while the other keeps flowing: the first sign of a codec or stream-set
// change, before ErrInputChanged.
// A plain input stall stops both streams; only a stream that keeps arriving
// (three buffers since the wait began) marks the waited one as starving.
func (d *continuousDecoder) Starving() time.Duration {
	since := d.waitingSince.Load()
	if since == 0 {
		return 0
	}
	other := d.otherStream.Load()
	if d.reads[other].Load()-d.otherAtWait.Load() < 3 {
		return 0
	}
	return time.Since(time.Unix(0, since))
}

func (m *Manager) StartDecoder(ctx context.Context, r DecoderRequest) (Decoder, error) {
	if e := validateRaw(r.Format); e != nil {
		return nil, e
	}
	// Provider credentials and playlist rewriting belong to livetv's grantInput.
	// Refuse a raw remote input rather than accidentally expose it through argv.
	if len(r.InputHeaders) > 0 {
		return nil, errors.New("transcode: grant input headers through the loopback relay before decoding")
	}
	u, e := url.Parse(r.InputURL)
	if e != nil {
		return nil, errors.New("transcode: invalid decoder input")
	}
	if u.Scheme != "" && !filepath.IsAbs(r.InputURL) {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || ip == nil || !ip.IsLoopback() || u.User != nil {
			return nil, errors.New("transcode: decoder requires a local file or loopback HTTP grant")
		}
	}
	if r.Loop && (r.Live || (u.Scheme != "" && !filepath.IsAbs(r.InputURL))) {
		return nil, errors.New("transcode: Loop requires a local non-live file")
	}
	if r.InputURL == "" || r.Offset < 0 {
		return nil, errors.New("transcode: invalid decoder input or offset")
	}
	sockets := make([]*rawSocket, 0, 2)
	for range 2 {
		s, e := newRawSocket()
		if e != nil {
			for _, s := range sockets {
				s.close()
			}
			return nil, e
		}
		sockets = append(sockets, s)
	}
	ok := false
	defer func() {
		if !ok {
			for _, s := range sockets {
				s.close()
			}
		}
	}()
	f := r.Format
	if r.Seeked && (r.Live || r.Loop) {
		return nil, errors.New("transcode: indexed input must be non-live and non-looping")
	}
	args := []string{"-hide_banner", "-loglevel", "warning", "-nostdin", "-threads", "2", "-filter_threads", "1"}
	args = append(args, inputSafetyArgs(r.InputURL, r.Live)...)
	// Keep decode off the shared CPU on Apple hosts. Frames return to system
	// memory for the compositor; unsupported source codecs retain software decode.
	if m.hw.Name == AccelVideoToolbox {
		args = append(args, "-hwaccel", "videotoolbox")
	}
	if r.Live {
		args = append(args, liveDecoderInputArgs(r, u)...)
	}
	if !r.Live && !r.Seeked && r.Offset > 0 {
		args = append(args, "-ss", strconv.FormatFloat(r.Offset.Seconds(), 'f', 6, 64))
	}
	if r.Loop {
		args = append(args, "-stream_loop", "-1")
	}
	if r.Seeked {
		args = append(args, "-probesize", "262144", "-analyzeduration", "500000")
	}
	videoFilter := fmt.Sprintf("scale=%d:%d,fps=%d/%d,setsar=1,", f.Width, f.Height, f.FPSNum, f.FPSDen)
	if r.Live {
		// Broadcast feeds are often interlaced and sometimes 4:3 or anamorphic.
		// Deinterlace field by field (1080i50 airs as 50p), then fit the display
		// aspect inside the working frame instead of stretching it.
		videoFilter = liveVideoJumps(f, liveForwardJump(r, u)) + fmt.Sprintf("bwdif=mode=send_field:deint=interlaced,scale=w='if(gte(dar,%[1]d/%[2]d),%[1]d,max(2,2*trunc(%[2]d*dar/2)))':h='if(gte(dar,%[1]d/%[2]d),max(2,2*trunc(%[1]d/dar/2)),%[2]d)',setsar=1,pad=%[1]d:%[2]d:(ow-iw)/2:(oh-ih)/2,fps=%[3]d/%[4]d,", f.Width, f.Height, f.FPSNum, f.FPSDen)
	}
	audioFilter := "aresample=48000:async=1:first_pts=0"
	if r.Seeked && r.Offset > 0 {
		trim := strconv.FormatFloat(r.Offset.Seconds(), 'f', 6, 64)
		videoFilter = "trim=start=" + trim + "," + videoFilter
		audioFilter = "atrim=start=" + trim + ",asetpts=PTS-STARTPTS," + audioFilter
	}
	pts := make(chan time.Duration, 16)
	ptsStop := make(chan struct{})
	if r.Live {
		args = append(args, "-loglevel", "info")
		videoFilter += "showinfo=checksum=0,"
		// Audio keeps its source clock (no first_pts) so the assembler can lock
		// it to the pictures; ashowinfo reports where it starts.
		audioFilter = liveAudioJumps(liveForwardJump(r, u)) + "ashowinfo," + "aresample=48000:async=1"
	}
	videoFilter += fmt.Sprintf("setpts=N*%d/(%d*TB)", f.FPSDen, f.FPSNum)
	args = append(args, "-i", r.InputURL, "-map", "0:v:0", "-vf", videoFilter, "-pix_fmt", "yuv420p", "-c:v", "rawvideo", "-threads:v", "1", "-f", "rawvideo", sockets[0].url(), "-map", "0:a:0", "-af", audioFilter, "-c:a", "pcm_s16le", "-ar", "48000", "-ac", "2", "-f", "s16le", sockets[1].url())
	d := &continuousDecoder{readDone: make(chan struct{}), ptsStop: ptsStop}
	d.audioStart.Store(-1)
	p, e := m.startContinuous(ctx, args, "", true, sockets, nil, nil, func(line string) {
		if !r.Live {
			return
		}
		if !strings.Contains(line, "showinfo") && (strings.Contains(line, "rror") || strings.Contains(line, "Invalid data")) {
			d.errorLines.Add(1)
		}
		if strings.Contains(line, "Parsed_ashowinfo") {
			if at, ok := liveFramePTS(line); ok && at >= 0 {
				d.audioStart.CompareAndSwap(-1, int64(at))
			}
			return
		}
		stamp, ok := liveFramePTS(line)
		if !ok {
			return
		}
		select {
		case pts <- stamp:
		case <-ptsStop:
		case <-ctx.Done():
		case <-m.stop:
		}
	})
	if e != nil {
		return nil, e
	}
	capacity := 8
	if r.Live {
		capacity = 0
	}
	d.continuousProcess, d.frames = p, make(chan RawFrame, capacity)
	// Read both sockets independently: muxer scheduling may deliver many audio
	// blocks before the next picture. Bounded queues propagate prewarm pressure.
	vq := make(chan []byte, 8)
	aq := make(chan []byte, 32)
	for i, ch := range []chan []byte{vq, aq} {
		d.workers.Add(1)
		go func(s *rawSocket, ch chan []byte, video bool) {
			defer d.workers.Done()
			defer close(ch)
			c, e := s.accept()
			if e != nil {
				return
			}
			for n := int64(0); ; n++ {
				size := tickPCM(f, n)
				if video {
					size = f.Width * f.Height * 3 / 2
				}
				b := make([]byte, size)
				if _, e := io.ReadFull(c, b); e != nil {
					return
				}
				d.reads[i].Add(1)
				select {
				case ch <- b:
				case <-p.ctx.Done():
					return
				}
			}
		}(sockets[i], ch, i == 0)
	}
	d.workers.Add(1)
	go func() {
		defer d.workers.Done()
		defer close(d.readDone)
		defer close(d.frames)
		watch := r.Live
		paired := false
		fill := liveFill{fps: f.FPSNum / max(1, f.FPSDen)}
		var pcm []byte
		ticks := int64(0)
		for {
			v, err := d.receive(p, vq, aq, watch && paired, liveVideoStarved)
			if err != nil {
				d.readErr = err
				return
			}
			stamp := time.Duration(0)
			if r.Live {
				select {
				case stamp = <-pts:
				case <-p.ctx.Done():
					d.readErr = p.result()
					return
				}
			}
			if r.Live && !paired {
				// Lock sound to picture at the source: sample 0 must be the
				// first picture's source time, whatever each stream's start.
				if err := d.alignAudio(p, aq, vq, &pcm, stamp); err != nil {
					d.readErr = err
					return
				}
			}
			for len(pcm) < tickPCM(f, ticks) {
				b, err := d.receive(p, aq, vq, watch && paired, liveAudioStarved)
				if err != nil {
					d.readErr = err
					return
				}
				pcm = append(pcm, b...)
			}
			a := append([]byte(nil), pcm[:tickPCM(f, ticks)]...)
			pcm = pcm[tickPCM(f, ticks):]
			ticks++
			paired = true
			frame := RawFrame{Video: v, PCM: a, SourcePTS: stamp, SourcePTSValid: r.Live && stamp >= 0}
			out := []RawFrame{frame}
			if r.Live {
				out = fill.next(frame, time.Now())
			}
			for _, frame := range out {
				select {
				case d.frames <- frame:
				case <-p.ctx.Done():
					d.readErr = p.result()
					return
				}
			}
		}
	}()
	ok = true
	return d, nil
}

// A codec change on a live PID (AAC to AC3, or a new video codec) leaves the
// decoder chosen at open feeding undecodable packets: the other elementary
// stream keeps arriving while this one stops. ffmpeg neither exits nor
// recovers, so the output would go silent (or frozen) for many seconds.
// Waiting this long with the other queue this full means the input changed.
const (
	liveStarvedAfter = 600 * time.Millisecond
	liveVideoStarved = 16 // audio ticks queued while no picture arrives (~0.5 s)
	liveAudioStarved = 6  // pictures queued while no audio arrives
)

// receive takes the next buffer from ch. With watch set it reports
// ErrInputChanged when ch stays empty while other holds at least starved
// buffers for liveStarvedAfter.
func (d *continuousDecoder) receive(p *continuousProcess, ch, other chan []byte, watch bool, starved int) ([]byte, error) {
	var poll <-chan time.Time
	if watch {
		t := time.NewTicker(liveStarvedAfter / 6)
		defer t.Stop()
		poll = t.C
	}
	select {
	case b, open := <-ch:
		if !open {
			return nil, p.result()
		}
		return b, nil
	default:
	}
	if watch {
		stream := int64(0)
		if starved == liveVideoStarved {
			stream = 1 // waiting for pictures; sound is the other stream
		}
		d.otherStream.Store(stream)
		d.otherAtWait.Store(d.reads[stream].Load())
		d.errorsAtWait.Store(d.errorLines.Load())
		d.waitingSince.Store(time.Now().UnixNano())
		defer d.waitingSince.Store(0)
	}
	var since time.Time
	for {
		select {
		case b, open := <-ch:
			if !open {
				return nil, p.result()
			}
			return b, nil
		case <-p.ctx.Done():
			return nil, p.result()
		case now := <-poll:
			if len(other) < starved {
				since = time.Time{}
				continue
			}
			if since.IsZero() {
				since = now
			} else if now.Sub(since) >= liveStarvedAfter {
				return nil, ErrInputChanged
			}
		}
	}
}

// Live inputs keep their source clock (-copyts) so a reopened input can
// recognise media it already aired. A provider clock jump or reset must not
// reach fps or aresample on that clock: they would fill a +5000 s jump with
// hours of repeated pictures and silence, and drop everything after a reset.
// Each stream continues its own timeline across a backwards step or a forward
// step over 10 s (ffmpeg's dts_delta_threshold); shorter forward gaps are
// packet loss and stay, so fps and aresample keep pictures and sound in sync.
//
// A forward step that only one stream takes (pictures lost, sound intact)
// must stay filled: closing it on that stream alone would shift sound
// against picture for the rest of the session. A lost playlist segment
// gaps both streams and is skipped whole by the assembler (liveFill).
func liveAudioJumps(forward float64) string {
	return fmt.Sprintf("asetpts='if(eq(N,0),PTS,if(gt((PTS-PREV_INPTS)*TB,%g)+lt((PTS-PREV_INPTS)*TB,-0.5),PREV_OUTPTS+NB_SAMPLES/(SR*TB),PREV_OUTPTS+PTS-PREV_INPTS))',", forward)
}

func liveVideoJumps(f RawFormat, forward float64) string {
	return fmt.Sprintf("setpts='if(eq(N,0),PTS,if(gt((PTS-PREV_INPTS)*TB,%g)+lt((PTS-PREV_INPTS)*TB,-0.5),PREV_OUTPTS+%d/(%d*TB),PREV_OUTPTS+PTS-PREV_INPTS))',", forward, f.FPSDen, f.FPSNum)
}

// liveForwardJump is the forward timestamp step a live input closes rather
// than fills: ffmpeg's dts_delta_threshold.
func liveForwardJump(DecoderRequest, *url.URL) float64 { return 10 }

func liveHLS(r DecoderRequest, u *url.URL) bool {
	return r.InputFormat == "hls" || (r.InputFormat == "" && strings.HasSuffix(strings.ToLower(u.Path), ".m3u8"))
}

// liveDecoderInputArgs are the input options of a live provider decoder.
// The loopback relay owns provider connections; ffmpeg resumes a dropped
// relay read in-process and bounds a read that never returns. Damaged
// pictures keep libavcodec's error concealment: dropping corrupt packets
// measured no better and lost whole GOPs on packet loss.
func liveDecoderInputArgs(r DecoderRequest, u *url.URL) []string {
	// The tune's probe already analysed these bytes, and they are still in the
	// relay's buffer: a live decoder must not wait out ffmpeg's default five
	// seconds (or any long bound) of real-time media again before decoding.
	args := []string{"-copyts", "-fflags", "+genpts", "-probesize", "5000000", "-analyzeduration", "1000000"}
	if u.Scheme == "http" {
		args = append(args, "-rw_timeout", "5000000", "-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_on_network_error", "1", "-reconnect_delay_max", "2")
	}
	switch {
	case liveHLS(r, u):
		start := "-1"
		if r.ReserveStart {
			start = "0"
		} else if r.PrimeLive {
			start = "-4"
		}
		args = append(args, "-live_start_index", start)
	case r.InputFormat == "mpegts":
		// Keep stream identity across PMT version bumps that add or drop
		// unrelated PIDs; a real codec change is caught by starvation.
		args = append(args, "-merge_pmt_versions", "1")
	}
	return args
}

func (d *continuousDecoder) Next(ctx context.Context) (RawFrame, error) {
	select {
	case <-ctx.Done():
		return RawFrame{}, ctx.Err()
	case <-d.ctx.Done():
		return RawFrame{}, d.ctx.Err()
	default:
	}
	select {
	case <-ctx.Done():
		return RawFrame{}, ctx.Err()
	case <-d.ctx.Done():
		return RawFrame{}, d.ctx.Err()
	case f, ok := <-d.frames:
		if !ok {
			return RawFrame{}, d.readErr
		}
		return f, nil
	}
}
func (d *continuousDecoder) Close() error {
	d.ptsOnce.Do(func() { close(d.ptsStop) })
	d.continuousProcess.close()
	d.workers.Wait()
	return nil
}

// Diagnostics reports bounded decoder progress without exposing the input URL.
func (d *continuousDecoder) Diagnostics() string {
	return fmt.Sprintf("videoFrames=%d audioTicks=%d queued=%d stderr=%s", d.reads[0].Load(), d.reads[1].Load(), len(d.frames), d.stderr.String())
}

func liveFramePTS(line string) (time.Duration, bool) {
	if !strings.Contains(line, "showinfo") || !strings.Contains(line, " n:") {
		return 0, false
	}
	_, tail, ok := strings.Cut(line, " pts_time:")
	if !ok {
		return 0, false
	}
	fields := strings.Fields(tail)
	if len(fields) == 0 {
		return 0, false
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || value < 0 || value > 1e12 {
		return -1, true
	}
	return time.Duration(value * float64(time.Second)), true
}

// liveFill skips media a live input never had. When both streams lose the
// same span (a provider segment that failed), ffmpeg fills it on each stream
// independently: repeated pictures from fps, digital silence from aresample.
// Played, that is a held picture and silence; skipping it whole (picture and
// sound of every filled tick together) keeps them in sync and the viewer's
// picture moving, spending reserve instead. Filler arrives as a burst, faster
// than real time; a genuinely still and silent programme arrives at real
// time and is kept.
type liveFill struct {
	fps      int
	prev     []byte
	pending  []RawFrame
	since    time.Time
	skipping bool
	skipped  int
}

func (l *liveFill) next(frame RawFrame, now time.Time) []RawFrame {
	filler := l.prev != nil && silentPCM(frame.PCM) && bytes.Equal(frame.Video, l.prev)
	l.prev = frame.Video
	if !filler {
		l.skipping = false
		out := append(l.pending, frame)
		l.pending = nil
		return out
	}
	if l.skipping {
		l.skipped++
		return nil
	}
	if len(l.pending) == 0 {
		l.since = now
	}
	l.pending = append(l.pending, frame)
	run := max(1, l.fps/2)
	if len(l.pending) < run {
		return nil
	}
	// Half a second of filler: skip it if it came clearly faster than it
	// would play (a quarter faster; real-time media arrives at 1x).
	if now.Sub(l.since)*5 < time.Duration(len(l.pending))*time.Second*4/time.Duration(max(1, l.fps)) {
		l.skipping = true
		l.skipped += len(l.pending)
		l.pending = nil
		return nil
	}
	out := l.pending
	l.pending = nil
	return out
}

func silentPCM(pcm []byte) bool {
	for _, b := range pcm {
		if b != 0 {
			return false
		}
	}
	return len(pcm) > 0
}

// liveAlignLimit bounds the opening correction; a larger start offset is a
// broken stream, not an A/V start difference.
const liveAlignLimit = 2 * time.Second

// alignAudio trims or pads the opening sound so its first sample plays with
// the first picture: the source's own A/V offset, kept to the sample.
func (d *continuousDecoder) alignAudio(p *continuousProcess, aq, vq chan []byte, pcm *[]byte, video time.Duration) error {
	for len(*pcm) == 0 {
		b, err := d.receive(p, aq, vq, false, 0)
		if err != nil {
			return err
		}
		*pcm = append(*pcm, b...)
	}
	for wait := time.Now().Add(250 * time.Millisecond); d.audioStart.Load() < 0 && time.Now().Before(wait); {
		time.Sleep(5 * time.Millisecond)
	}
	audio := d.audioStart.Load()
	if audio < 0 || video < 0 {
		return nil
	}
	offset := time.Duration(audio) - video
	if offset > liveAlignLimit || offset < -liveAlignLimit {
		return nil
	}
	const frame = 4 // s16le stereo
	samples := int(offset.Seconds()*48000) * frame
	if samples > 0 {
		// Sound starts after the first picture: lead with silence.
		*pcm = append(make([]byte, samples), *pcm...)
		return nil
	}
	for drop := -samples; drop > 0; {
		if len(*pcm) == 0 {
			b, err := d.receive(p, aq, vq, false, 0)
			if err != nil {
				return err
			}
			*pcm = append(*pcm, b...)
		}
		n := min(drop, len(*pcm))
		*pcm, drop = (*pcm)[n:], drop-n
	}
	return nil
}
