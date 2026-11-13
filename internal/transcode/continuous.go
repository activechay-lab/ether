package transcode

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// RawFormat is the switcher's working format. It is fixed for a channel output's life.
type RawFormat struct {
	Width, Height  int    // even; the channel's largest rendition size
	FPSNum, FPSDen int    // a broadcast rate: 24, 25, 30, 50 or 60 (and the /1001 NTSC forms)
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
	// GraphicsTransition marks the first animated frame replacing a title plate.
	GraphicsTransition bool
	OutputTime         time.Time // scheduled output-clock time, independent of delivery/burst time
	// PolicyTags are immutable comparable identities supplied by the planner.
	// Every identity represented in a segment survives encoder publication.
	PolicyTags []any
	// SourcePTS is the original live input presentation time, before re-timing
	// onto the output clock. Live decoders always set SourcePTSValid; encoders
	// ignore this metadata. It detects stale media across provider reconnects.
	SourcePTS      time.Duration
	SourcePTSValid bool
	RepeatVideo    bool   // encoder may repeat this picture at a lower input cadence
	Video          []byte // len = W*H*3/2
	PCM            []byte // len = samples*Channels*2
}

type DecoderRequest struct {
	SessionID    string
	InputURL     string // playback input or loopback-granted provider URL; never put on a command line raw
	InputHeaders http.Header
	Format       RawFormat // decoder scales, frame-rate converts and resamples to this
	Offset       time.Duration
	// Seeked marks a trusted indexed projection already starting at a keyframe.
	// Offset is then a small forward trim, without another demuxer seek.
	Seeked bool
	Live   bool // IPTV/live input: no seeking, no prewarm hold
	// PrimeLive reads four provider segments on the first open only. Callers
	// with an aired source timestamp must leave it false on reconnect.
	PrimeLive bool
	// ReserveStart starts an HLS live input at the first segment the relay
	// lists, which is the stability reserve, instead of near the live edge.
	ReserveStart bool
	// InputFormat is the probed live container ("hls" or "mpegts"; empty when
	// unknown). Demuxer-private options are only valid for their own demuxer.
	InputFormat string
	Loop        bool // repeat a local cached master seamlessly; incompatible with Live and URLs
	// AudioIndex is the input's audio track to decode, counted among its
	// audio streams (0 = first). Callers pick it by language.
	AudioIndex int
}

// ErrInputChanged reports that a live input changed its codec or stream set
// mid-stream (a PMT version bump, or another codec on the same PID): one
// elementary stream keeps flowing while the other can no longer be decoded.
// The caller reopens the same source at its live edge.
var ErrInputChanged = errors.New("transcode: live input changed its streams")

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
	Source          RawFormat       // what Write receives
	Decision        core.Decision   // output codec, size, bitrate, audio; fixed for life; HW from HWAccel
	ClientCaps      core.ClientCaps // viewer limits used for optional RTX VSR upscale headroom
	SegmentDuration time.Duration   // 4 s; keyframe exactly at every segment start
	DVRWindow       time.Duration   // segments kept on node-local disk for timeshift (30 min default; 0 = live window only)
	// LiveSource marks a provider (IPTV) channel: rate control may use one
	// segment of VBV buffer under the ceiling, so keyframes of moving
	// pictures are not starved. Scheduled channels keep a one-second bound.
	LiveSource bool
}

// ContinuousEncoder never restarts and never changes output parameters.
// Its timestamps come from the tick counter (monotonic, no discontinuities).
// Write takes exactly one tick and may block during encoder scheduling dips.
// Callers buffer ticks independently of the output clock, preserving PCM while
// repeating pictures under pressure. Context cancellation interrupts a write.
// Segments exposes finished media for Live TV to build viewer responses.
type ContinuousEncoder interface {
	Write(ctx context.Context, f RawFrame) error
	Segments() SegmentIndex
	Close() error
}

// Segment is one finished segment. Seq is contiguous from 0 for the
// encoder's life; Start is the wall-clock time of its first frame
// (PROGRAM-DATE-TIME); Duration is exact (120 frames at 30000/1001 = 4.004 s).
type Segment struct {
	PolicyTags []any
	Seq        int64
	Start      time.Time
	Duration   time.Duration
}

// SegmentIndex is safe for concurrent use. Only finished segments are listed.
// Segments older than DVRWindow are evicted; Window never returns evicted
// segments, and serving one returns core.ErrNotFound.
type SegmentIndex interface {
	Latest() (Segment, bool)                                     // newest finished segment
	Window(from, until time.Time) []Segment                      // segments overlapping [from, until), oldest first
	Since(seq int64, max int) []Segment                          // up to max segments with Seq > seq
	Wait(ctx context.Context, after int64) (Segment, error)      // blocks until segment after+1 is finished
	ServeTS(w http.ResponseWriter, r *http.Request, seq int64)   // MPEG-TS segment (HLS)
	ServeInit(w http.ResponseWriter, r *http.Request)            // fMP4 init (DASH)
	ServeFMP4(w http.ResponseWriter, r *http.Request, seq int64) // fMP4 media segment (DASH)
	AttachTS(ctx context.Context) (io.ReadCloser, error)         // live continuous TS from the live edge; bounded fan-out
	Format() (codecs string, width, height int, bandwidth int)   // for playlists and MPD Representation attributes
}

type ContinuousRunner interface {
	StartDecoder(ctx context.Context, r DecoderRequest) (Decoder, error)
	StartEncoder(ctx context.Context, r EncoderRequest) (ContinuousEncoder, error)
}
