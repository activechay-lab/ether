Transcode chaos (on demand; not part of default CI)

Run from the repository root with a local Go toolchain and the pinned ffmpeg:

  go run ./cmd/transcodechaos -mode short -corpus \
    -ffmpeg /path/to/ffmpeg -ffprobe /path/to/ffprobe -report chaos.json

Use -mode full for generated inputs, source faults, client sequences,
VideoToolbox failure injection, Live TV and the larger public-media matrix.
The command prints a scenario/path scoreboard and saves JSON. Unexpected
failures exit nonzero. EXPECTED_ERROR means permanently missing source bytes
were rejected promptly, with cleanup checked; XFAIL means a pinned stock
FFmpeg limitation was also rejected promptly by Ether. Neither is a playback
success. SKIP is an explicit coverage gap, not a pass.

-cache-dir selects a cache outside the repository with a 10 GB hard limit.
Public samples download only with -corpus, are verified against pinned hashes,
and are not redistributed. See corpus.go for upstream licence/usage links.
Netflix samples are trimmed locally to 30 seconds; conformance archives are
extracted with a size limit and muxed without re-encoding. -fixture-dir keeps
fixtures and raw offline diagnostic events outside the repository. Scenarios
run serially with a small Go CPU budget to limit load on shared machines.

Local mode exercises the transcode manager and real Plex handler fixtures
with loopback sources. It checks ffprobe/FFmpeg decode, timing, resource
cleanup, bounded memory/disk, generated damage, HTTP source faults, HLS TS
and fMP4, DASH, progressive Matroska, copy remux and download conversion.
The public subset includes official Matroska, GStreamer, Chromium, ITU AVC
and HEVC conformance, Netflix HDR, Apple HLS and DASH-IF media. Apple and
DASH-IF relay vectors are replayed from the cache over loopback, including
encoder interruption, rather than fetched live by Ether from the internet.

The full corpus client driver applies seek forward/backward, sampled scrubs
and fast-forward, pause/ping/resume, near-end playback, quality changes, and
available audio/subtitle changes across the streaming paths. These implement
gst-validate action concepts through Plex requests, not the GStreamer player
itself. Download conversion is a finite copy-only job, not a seekable session.
Very short conformance clips cannot run seek scenarios. HLS follows the
container actually negotiated; a HEVC target can be fMP4 even in the generic
HLS case. See each JSON row for actual coverage.

For a separate Ether lab server, pass -server-url URL and -media-key KEY
(or comma-separated keys) and set ETHER_CHAOS_TOKEN in the environment. Stage
short test titles through that lab's configured source first. The driver
checks Ether's health identity, refuses foreign media origins/redirects,
stops its own sessions, and removes only its own download-queue items.
Full lab mode drives HLS TS/fMP4, DASH, progressive MKV and download requests;
local fault injection and the full corpus client sequences use the isolated
handler fixtures. The URL mode does not upload or register corpus assets:
Ether has no local-media ingestion API. No external lab URL was available
for this run; origin/credential containment is checked with loopback fakes.
Never point it at a production server or a real Plex server.

expected-failures.json records only digest-pinned stock decoder limitations.
Currently ITU AVC FMO (FM2_SVA_C) is unsupported by the pinned FFmpeg. An
unlisted stock failure or a newly decodable expected failure requires triage.
Reports retain unresolved playable-source failures; do not silently add them
to the stock-decoder list.

Coverage limits: VAAPI/NVENC runtime and physical Shield/LG colour validation
require the corresponding hardware; local graph checks do not replace them.
Chromium supplies encoded DV5/DV8; Netflix's available DV/Atmos production
masters are not small packaged delivery streams suitable for this 10 GB tier.
PGS packet generation is unavailable with the pinned encoder. Jellyfin/Silo
HTTP endpoint matrices, automatic remote corpus staging, actual client rate
control, and exhaustive gst-validate scenarios remain outside this verified
matrix. A progressive hardware failure aborts at a complete cluster and a
new viewer-initiated request uses CPU; it is not seamless automatic resume.

See reports/corpus-tier2-before.json, reports/corpus-tier2-after.json and the
accompanying observations for measured results and remaining failures.

Live TV acceptance (v0.3.6 live reliability)

-live runs real Ether against a scripted fake IPTV provider and judges what
a viewer would see. Nothing leaves the machine: the provider, Ether (Live TV
service, transcode manager and Plex server, in this process, in-memory
database) and the players are all loopback and stop when the run ends.

  quick subset (6 rows, about 1.5 minutes):
    go run ./cmd/transcodechaos -live quick \
      -ffmpeg $FF/ffmpeg -ffprobe $FF/ffprobe -live-fixture-ffmpeg ffmpeg \
      -live-parallel 6 -report live-quick.json -live-markdown live-quick.md

  full matrix (66 rows; about 11 minutes with -live-parallel 6, 16 with 4):
    go run ./cmd/transcodechaos -live full -live-parallel 6 \
      -ffmpeg $FF/ffmpeg -ffprobe $FF/ffprobe -live-fixture-ffmpeg ffmpeg \
      -live-baseline docs/acceptance/036-live-reliability/baseline.json \
      -report live-full.json -live-markdown live-full.md

$FF is the pinned build (scripts/fetch-ffmpeg.sh with FFMPEG_OUT set to a
directory outside the repository). Fixtures may use any recent ffmpeg; they
are cached under -cache-dir. -live-only scenario/mode substrings select rows,
-live-preset fastest|balanced|rocksolid sets and judges the stability preset,
-live-observe sets the watch time per row, -live-keep keeps each row's Ether
log and captured segments, -live-client plex-firetv tunes as Fire TV and
judges with ExoPlayer's 3.5x target-duration rule (protocol judge only), and
-live-render rewrites the Markdown from an existing JSON report. The v0.3.5
baseline and how it was produced are in docs/acceptance/036-live-reliability.

Provider (live_provider.go): live HLS (target duration 4 s, 6-segment backlog)
or real-time raw MPEG-TS, generated from synthetic H.264/AAC fixtures cut at
keyframes. Their loop period is a whole number of video, AAC and AC3 frames,
so loops are continuous in timestamps and continuity counters. Faults run on
a timeline: playlist stall, segment 404/5xx/slow, TCP reset, clean EOF, body
stall, 403/458/509 refusals, account connection limit, AAC->AC3 on the same
PID with and without a PMT version bump, resolution change, clock jump and
reset, 10 s GOP, keyframe loss, corrupt and lost packets, provider HLS with or
without its own discontinuity tags, and a backup source that is healthy,
absent or also failing.

Ether is tuned through the Plex native live tune (POST
/livetv/dvrs/1/channels/{id}/tune with Apple TV headers, the
plex-native-live-tune quirk path) and the returned client playlist is played.
Modes: passthrough (provider HLS), relay-copy (raw TS), relay-transcode
(channel Transcode, no-signal screen "error") and steady (Transcode with the
"card" no-signal screen, i.e. broadcast mode).

Judges:
- Protocol (everywhere): polls the client playlist every 0.2 s, downloads
  every segment, and records the longest time without a new segment, EXTINF
  and measured segment length against the target duration, codec changes
  between consecutive segments without EXT-X-DISCONTINUITY (ffprobe), media
  and discontinuity sequence consistency across the sliding window, the
  time Ether took to answer the first playlist (a whole target duration
  fails: AVFoundation then leaves the item stuck with no error and never
  requests a segment; half of one warns), HTTP
  errors, decode errors, and per-segment content: the fixture has a green
  band on top, so frames without it are an Ether screen or black; held frames
  (freezedetect) and silence (silencedetect). Rows on the sync fixture
  (hls-video-drop) also measure lip sync: a corner flash and a 3 kHz beep
  start together every second, and any pulse with audio over 45 ms early
  or 125 ms late in the published media fails the row (ITU-R BT.1359).
- AVFoundation (macOS, tools/avprobe, built on demand with swiftc): AVPlayer
  frame delivery, frames with/without the green band, repeated frames, item
  failures and CoreMedia error-log codes. Run the harness outside any sandbox
  or every item fails with -9405. Audio is judged from the published
  segments; in-player audio taps do not attach to HLS items. With
  -live-keep, player-http.log records every request the player made and
  each playlist it received, through a loopback tap (AVPlayer has no access
  log until an item is ready).

Each row reports freeze, slate (screen), silence, client errors, recovered
and TTFF (tune to the first provider picture), and passes only when the
common rules (no client error, recovered, no gap over 1.5x target duration,
no overlong segment, consistent sequences, first playlist answered within
one target duration, TTFF <= 5 s) and the scenario's
per-preset budget in live_scenarios.go all hold.

Raw TS outage rows (ts-stall-8s, ts-stall-8s-burst8, ts-refuse-403/458/509)
are judged by the owner's decision of 2026-11-07, no playout slowdown and no
startup wait, so a raw TS reserve exists only as far as the provider bursts
backlog on connect. Without a burst: slate <= outage + 2 s and freeze <=
0.5 s. With one: screen <= max(0, outage - (burst - start window)) + 2 s,
where the burst is measured by the fake provider on the first connect and
the start window is the media in the first published playlist. Silence may
run 0.5 s past the allowance. All common rules still apply. The original
budget is still reported per row as "Ideal" (verdict marked *).
-live-rescore re-judges an existing -report in place with the current
criteria (and rewrites -live-markdown), without re-running.

Not covered: the Jellyfin live path. Its stream URL comes from the app's
session broker, capability builder and identity, which only internal/app
assembles; driving it needs the full binary with a local viewer. Plex Web,
the Plex tvOS app itself and Fire TV hardware remain manual device rounds. Rows run in parallel on a
shared host; slow judge polls are reported and not charged to Ether. Use
-live-parallel 1 for timing-critical measurements.
