# Shared look playout acceptance

P5 plays cached ident (cold output and scheduled), up-next, tonight, break,
back, off-air and converted reel masters through bounded local decoders.
Saved reel metadata is refreshed in the background through the real On Air
service adapter. Schedule uses durations and a saved-revision anchor; sequence
and seeded shuffle selections replay the same history across overlapping
windows, respect repeat windows and never cut a reel clip. A fixed plugin clock
reservation replaces a crossing partial reel with gap fill. Missing masters
are queued and skipped once per airing, including a failure between cache
preflight and decoder preparation.

The frame clock reads background schedule snapshots. Bug and lower-third
scenes resolve assets and rasterize outside that clock. Compositing modifies
an exclusively owned input frame once, then shares it immutably across
renditions. Lower thirds fade during the first eight seconds and again at the
programme midpoint. Every virtual channel is encoded, so both overlay policies
apply. The bug and at most sixteen programme cards are cached per planner.

Signal off-air and can't-play inputs use the live engine; the P4 looping screen
master path is removed. One bounded screen input remains prewarmed while a
programme plays. An input stall cuts to it at 1.5 seconds. Cold probe/seek has a
separate 45-second bound and remains on the screen until the first programme
frame. First/last-frame readiness belongs to each decoder, so a retry cannot
inherit the preceding decoder's stale timestamp. Bounded, redacted diagnostics
cover decoder starts, first-frame latency, raw video/audio counts, abandonment
and rendition backpressure.

## Memory and compositing cost

There are no full-loop frame rings. An eight-frame decoder prewarm queue is
24.88 MB at 1920×1080 I420 (11.06 MB at 1280×720). The switcher input queue holds
one frame (3.11 MB / 1.38 MB). The Signal input has one queued and one producing
frame; its static generator state is below 1 MB. A plate engine retains 7.26 MB
at 1080p for I420 and the vignette lookup. These are bounded buffer sizes, not
whole-process RSS measurements.

Prepared overlay blend plus a conservative preallocated whole-frame copy on the
Apple M4 Pro measured:

| Format | Time/tick | CPU cores at 30 fps | Blend allocations |
| --- | ---: | ---: | ---: |
| 1280×720 | 0.170 ms | 0.0051 | 0 |
| 1920×1080 | 0.380 ms | 0.0114 | 0 |

Production blends the owned decoder buffer in place, so does not need that copy.

## Cross-cutting remote input correction

The real lab exposed overlapping MKV header/cue/media reads cancelling one
another through the debrid read-ahead epoch. An additive optional
`core.ConcurrentRangeReaderAt` capability lets loopback demuxers retain urgent
work under each request's lifetime. Direct-player seeks retain their existing
supersession/connection-retirement behavior. A real-HTTP test fails before the
fix and passes afterwards; the existing VOD seek replay assertion is retained.
Raw decoding uses accurate seek, rather than preserving pre-seek keyframe
frames. No configuration model or migration was added in P5.

## Recordings and verification

`v035/samples/look-demo.mp4` and `.json`: 1920×1080, 52.032 seconds,
2382.191 kb/s. Actual On Air render queue/cache, uploaded-and-converted reel,
real decoder runners, one switcher/encoder, signed HLS client reads and copy
remux. Sequence: ident → programme with bug/lower third → break → reel → back
→ scheduled ident → programme. Visual frame strip inspected; zero
discontinuities, monotonic PTS/DTS and clean straight-through decode.

`v035/lab/pass/p5-virtual-real-film-4/recording.mp4`, `strip.png`, `report.json`:
actual debrid film at a deep join, 120 seconds, 32 contiguous segments, one codec
parameter set, zero discontinuities, monotonic DTS and no decode errors.
Ident-to-film transition visually verified. Programme preparation took 7.834 s
while the eight-second cold ident aired. P6 owns reducing deep-join latency.

Gates: build all, CGO-disabled CLI build, vet touched packages; scoped short
livetv/app/Plex suites; W9 clock input/unrated tests; no-look week golden;
overlapping reel windows/repeat/no-cut tests (with and without plugin clocks);
non-black all-candidates-fail continuous output; real slow range-served MKV
seek at more than 1h40 after a prior decoder stall; real HLS TS, DASH fMP4 and
Matroska straight-through continuity; complete debrid and playback packages.
Plex response shapes and Plex test assertions are unchanged in P5. W9's planner
test starts and waits for the background snapshot before its existing assertions.
