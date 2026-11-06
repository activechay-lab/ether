# Offline HLS splice verification

Build `go build ./cmd/splicecheck`. FFmpeg and FFprobe are explicit dependencies
of this offline tool, not new runtime dependencies. Media is never committed.

```
splicecheck -candidate /scratch/spliced -reference /scratch/uninterrupted \
  -boundary 3 -grid 4s -ffmpeg /tools/ffmpeg -ffprobe /tools/ffprobe
```

Directories contain numeric segment filenames and, for fMP4, `init.mp4`.
For generation-specific initialization segments or selected windows, use
`-input artifacts.json` with `Segments` and `Options` matching the exported
`internal/splicecheck` types. `Segment.InitPath` accompanies each `.m4s`.
`Options.SegmentDuration` is a Go duration in nanoseconds in JSON. For an
authored variable-duration playlist, supply `Options.Timeline` instead: an array
of title-time boundaries in nanoseconds, indexed by segment number, including
the terminal end boundary. For example, `[0,2000000000,4000000000,8000000000]`
describes two 2-second ramp segments followed by a 4-second segment. Obtain it
from the initial authored playlist, never from the replacement. The verifier
checks strictly increasing boundaries, candidate index coverage, start spacing
and each segment span (including a terminal or single segment) within one video
frame. Omitting `Timeline` preserves the constant-grid checks. The library
accepts a caller context and executable paths. Callers own their process timeout.

The verifier accepts clear H264 video and ADTS-representable AAC audio. It checks
the first VCL NAL is IDR, decodes each segment independently, and decodes their
byte concatenation with error-exit behavior. It preserves timestamps rather than
using the concat demuxer's timestamp rebasing. It compares decoded frame PTS and
counts with the reference, checks packet DTS, hashes each raw AAC AU after
stripping ADTS headers/CRC, checks audio packet clocks and checks segment-start
spacing against the grid within one video frame. The terminal partial segment
need not fill a grid slot. Video compression bytes need not equal the reference.

All checks and generation boundaries are JSON serializable. A missing reference
leaves frame-count and AU identity checks false and returns an error: independent
decode and increasing timestamps alone cannot prove absence of omitted media.
An operational error also prevents acceptance. A passing report describes only
its supplied files/window; it supplies no placement, fencing, client buffering,
source identity, database authority or latency evidence.

## Resume contract

A trusted `core.HLSRequest.Resume` names the first segment to regenerate and the
execution generation. Set `StartOffset` to zero. Generation must be nonzero.
The HLS manager rebuilds the original full-title zero-origin segment plan from
the same media information, segment duration and adaptive configuration, including
FastStart and its ramp lengths. It starts at `plan.Start(FirstSegment)` with
`-start_number FirstSegment`, retaining the original playlist and segment numbers.
For the default 2-second, 2-second, then 4-second plan, indices 0, 1, 2 and 3
start at 0, 2, 4 and 8 seconds. Reject indices outside the authored plan; checking
`N * SegmentDuration` would incorrectly reject valid tail segments. All replicas
must use the same planning inputs. `-ss`, timestamp offsets and forced keyframes
retain the authored timeline. Generation fencing belongs to the cluster consumer,
not the muxer. `temp_file` publication means an unfinished segment is regenerated;
only fully renamed, durably retained segments belong before the splice.

For uniform-grid sessions, the shared software H264 restart fix preserves the
fresh-start TS muxer clock.
`x264FramePlan` selects the encoder's B-frame/pyramid configuration and its decode
reorder depth together. At a TS restart, `-output_ts_offset reorder/fps` restores
the shift that FFmpeg applied to the initial negative DTS. Copied audio is retained
from `ceil(N*d*fps)/fps - reorder/fps`, matching the original TS interleave cutoff. The output
clock uses the demuxer's reference frame rate (or the negotiated output rate),
not a VFR source's average frame rate.

For fMP4, the existing preload is retained. The copied-audio restart cutoff is
`ceil(N*d*outputFPS)/outputFPS + ceil(2e6/sourceFPS)/1e6 - reorder/outputFPS`. For a VFR source the existing builder supplies no preload, so use zero for
that term. The microsecond rounding is
significant: an AU exactly on the nominal boundary can already belong to the
preceding fragment. No AAC bytes are re-encoded or rewritten by this fix.

Single-rung FastStart sessions keep a session-wide ramp clock through the steady
runs. The zero-reorder opening encode uses the steady encoder's clock lead without
adding B-frames; recovery uses the authored segment start in the audio cutoff.
fMP4 runs supply in-band headers so the retained ramp initialization segment can
decode the steady encode. These paths require their own ramp recovery matrix;
a passing uniform-grid check cannot certify them.

Chunked/keyframe-planned runs retain their previous mux clocks and audio cutoffs;
their slotter authors a separate timeline. Both restart correction helpers refuse
those run specifications, including a final chunked run with no remaining KeyTimes.

The measured recovery configuration is libx264 with copied LC AAC at 48 kHz.
Hardware encoder reorder depths, audio re-encoding identity and
other video codecs require separate proof. Ordinary video-copy arguments remain
unchanged. Copy-video HLS uses source keyframes: the session timeline snaps to
known keyframes; without a source index the muxer cuts at the next source
keyframe after its requested duration. Such a grid cannot be inferred from N*d;
cluster Resume currently refuses video copy. Normal client copy seeks keep their
existing behavior.

## Reproducing synthetic input arguments

`go build -tags splicelab ./cmd/splicelab` exposes the real argument builder only
in the lab build. Supply `-input`, ffprobe JSON via `-probe`, `-dir`, `-container`
(`mpegts` or `fmp4`) and `-first N`. It prints a JSON argument array, suitable for
an argv-based process runner. `-copy-video` exercises the ordinary remux builder.
Never execute that JSON through a shell. SIGKILL trials must record the exact
owned child, finalized filenames at death and first missing index; a normal
completed encode followed by concatenation does not prove a kill trial.

Use `-fast-start -ffmpeg /tools/ffmpeg` to drive the shipping manager through
the opening ramp and automatic steady-run transition. `-resume -first N` rebuilds
that same plan for recovery, including N=0. The lab writes the initial playlist,
`timeline.json`, numbered served media, and owned child PID/argv in `process.json`.
`-limit N` bounds collection at an exclusive index. Input is throttled only by
the tagged lab runner to make SIGKILL boundaries observable. Keep all these
artifacts in thread storage, outside the repository.
