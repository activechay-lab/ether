package transcode

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// RawFormat is the switcher's working format. It is fixed for a channel output's life.
type RawFormat struct {
	Width, Height  int    // even; the channel's largest rendition size
	FPSNum, FPSDen int    // 30/1 by default; 30000/1001 and 25/1 also allowed
	SampleRate     int    // 48000
	Channels       int    // 2
	PixelFormat    string // "yuv420p" (I420, planar, tightly packed: Y then U then V, no row padding)
}

// RawFrame is exactly one output tick: one video frame, plus the PCM for
// that tick (interleaved signed 16-bit little-endian). Samples per tick
// follow an exact accumulator: tick n carries
// floor((n+1)*SR*FPSDen/FPSNum) - floor(n*SR*FPSDen/FPSNum) samples per channel,
// so audio never drifts (1600 every tick at 48 kHz / 30 fps).
type RawFrame struct {
	Video []byte // len = W*H*3/2
	PCM   []byte // len = samples*Channels*2
}

type DecoderRequest struct {
	SessionID    string
	InputURL     string // playback input or loopback-granted provider URL; never put on a command line raw
	InputHeaders http.Header
	Format       RawFormat // decoder scales, frame-rate converts and resamples to this
	Offset       time.Duration
	Live         bool // IPTV/live input: no seeking, no prewarm hold
}

// Decoder yields frames already in Format, one per call, paced by the caller.
// Next blocks until a frame is ready; it returns io.EOF at end of input, and
// returns promptly with ctx.Err() on cancellation or after Close.
// The first frame after start is a keyframe of the input.
// Prewarm: a non-live decoder may be started ahead of time. It holds at most
// 8 decoded frames and then applies backpressure to ffmpeg (no unbounded
// buffering) until Next is called.
type Decoder interface {
	Next(ctx context.Context) (RawFrame, error)
	Close() error
}

type EncoderRequest struct {
	SessionID       string
	Source          RawFormat     // what Write receives
	Decision        core.Decision // output codec, size, bitrate, audio; fixed for life; HW from HWAccel
	SegmentDuration time.Duration // 4 s; keyframe exactly at every segment start
	DVRWindow       time.Duration // segments kept on node-local disk for timeshift (30 min default; 0 = live window only)
	HTTP            bool          // also produce a continuous byte stream for Plex for Mac
}

// ContinuousEncoder never restarts and never changes output parameters.
// Its timestamps come from the tick counter (monotonic, no discontinuities).
// Write takes exactly one tick and must not block longer than about one tick
// under normal load; sustained backpressure returns ErrEncoderBehind so P3
// can drop the rendition rather than stall the switcher.
// Output() implements core.HLSSession; it also implements core.DASHSession
// and, when HTTP is set, core.HTTPSession (type-assert).
type ContinuousEncoder interface {
	Write(ctx context.Context, f RawFrame) error
	Output() core.HLSSession
	Close() error
}

type ContinuousRunner interface {
	StartDecoder(ctx context.Context, r DecoderRequest) (Decoder, error)
	StartEncoder(ctx context.Context, r EncoderRequest) (ContinuousEncoder, error)
}

var ErrEncoderBehind = errors.New("continuous encoder is behind real time")
