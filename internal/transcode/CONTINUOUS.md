# Continuous playout runners

`Manager` implements `ContinuousRunner`. Start a decoder for each input, and
start an encoder for each rendition. `StartRenditions` validates a common raw
working format and rolls back partial starts. Scaling runs once inside each
rendition's ffmpeg, avoiding Go pixel scaling and additional decoders.

Go binds ephemeral IPv4 loopback listeners before spawning ffmpeg. ffmpeg
connects to them: no port handoff race, inherited descriptors or Windows
`ExtraFiles` dependency. Audio and video have independent bounded socket pumps.
`Write` copies its caller-owned buffers and waits at most one frame interval
for admission, returning `ErrEncoderBehind` if the rendition falls behind.
An admitted tick is immutable; a caller should drop a rendition on that error.

The existing guardian owns every ffmpeg process. Context cancellation, runner
Close and manager Close stop and reap the process; encoder slots remain reserved
until exit. `AcquireTranscodeSlot` exposes the existing shared admission budget
with an idempotent release. Output directories are removed after process exit
using the existing Windows sharing-violation retry helper.

Decoders accept local files or already granted loopback HTTP inputs. Provider
URLs and headers must first go through Live TV's input relay. Non-live inputs
hold eight queued frames; live inputs have no prewarm queue. Decoder frames and
PCM follow the canonical fractional-rate accumulator. `Loop` accepts local
non-live masters only and uses ffmpeg's in-process stream loop with a continuous
raw frame clock. The first input seek uses keyframe seeking; the caller owns
all subsequent pacing. A new decoder instance is the restart operation.

Each encoder encodes once, then tees the same packets to TS HLS segments,
muxed audio/video fMP4 HLS segments (for DASH delivery), and a continuous MPEG-TS
byte stream. Live TV owns viewer playlists, MPDs and any Matroska remux. The
index publishes only the prefix complete in both file formats. Its init contains
both tracks; `Format` reads their actual RFC 6381 codec identifiers from that
init. Sequence numbers begin at zero; wall-clock starts are anchored to the
first admitted tick. Durations are 4 seconds at 25/30 fps and 4.004 seconds at
30000/1001 (120 frames). A zero DVR window retains six segments; positive windows
retain enough whole segments to cover the requested duration. Evicted files
return HTTP 404, and evicted index waits return `core.ErrNotFound`.

`AttachTS` can attach at any time. Unattached bytes are discarded. Each reader
has a bounded 32-chunk queue; a slow reader is disconnected independently of the
encoder and other viewers. Byte-stream consumers begin decoding at the next
keyframe and receive repeating MPEG-TS programme tables.

## Repeat resource measurements

macOS and Linux, installed ffmpeg/ffprobe and `/usr/bin/time`:

```sh
nice -n 10 go test ./internal/transcode -run '^TestContinuousMeasurements$' -count=1 -v -timeout 240s -args -continuous-measure -continuous-workload all
```

A single 1080p30 software encoder:

```sh
nice -n 10 go test ./internal/transcode -run '^TestContinuousMeasurements$' -count=1 -v -args -continuous-measure -continuous-workload encoder -continuous-size 1920x1080 -continuous-encoder software -continuous-duration 10s
```

Linux RTX 4080 / NVENC:

```sh
nice -n 10 go test ./internal/transcode -run '^TestContinuousMeasurements$' -count=1 -v -args -continuous-measure -continuous-workload encoder -continuous-size 1920x1080 -continuous-encoder nvenc -continuous-duration 10s
```

Use `-continuous-workload h264` or `hevc` to measure a generated 1080p H.264 or
4K HEVC file respectively, scaled to `-continuous-size`. The default matrix
includes software/hardware 720p and 1080p, a 720p rendition from 1080p working
frames, and both decoders. Hardware names: `videotoolbox`, `nvenc`, `qsv`, `vaapi`.
Each measurement is serial and prints CPU cores, peak RSS in MiB and achieved fps.
CPU/RSS measure ffmpeg only; achieved fps also includes Go frame production and
copies. Synthetic inputs are a reproducible baseline, not a natural-footage
capacity guarantee. Linux sampling uses GNU time; macOS uses BSD time.

## Mac measurements (2026-11-05)

System ffmpeg 8.0.1; ten seconds per workload; all runs niced and serial.

| Workload | CPU cores | Peak RSS MiB | fps |
| --- | ---: | ---: | ---: |
| Software 720p30 | 0.141 | 104.3 | 29.97 |
| Software 1080p30 | 0.230 | 191.5 | 29.94 |
| VideoToolbox 720p30 | 0.050 | 62.8 | 29.98 |
| VideoToolbox 1080p30 | 0.088 | 102.1 | 29.98 |
| 720p rendition from 1080p working frames, software | 0.206 | 109.5 | 29.96 |
| 1080p H.264 decoder to 1080p30 | 0.084 | 126.9 | 29.97 |
| 4K HEVC decoder to 1080p30 | 0.471 | 288.9 | 29.78 |

The larger workloads initially exposed a deadlock in a sequential audio/video
handoff; both now pass after independent bounded transport queues. The extra
rendition measurement includes its scale, encoder and three mux outputs, and
excludes the shared decoder. Native scaling sustains 30 fps without Go scaling.

The lasting test feeds moving pattern, ten seconds of held picture, and another
moving pattern; checks fixed TS parameters, monotonic PTS/DTS and 4-second
segments; decodes HLS and muxed fMP4 straight through with `-xerror`; checks
segment-index timing, a 20-second HTTP decoder prewarm and a local loop crossing.
The offline index test covers fractional timing, shared TS/fMP4 eviction and
wait cancellation. This system ffmpeg lacks the DASH demuxer, so the fMP4 capture
is validated by concatenating init plus media rather than opening an MPD.
Playback defaults (120-second write-stall/idle budgets and 90-second engine
read timeout) exceed the 20-second prewarm hold; the real loopback HTTP hold
passes. A deployment overriding those budgets below 20 seconds must shorten its
prewarm lead accordingly.
