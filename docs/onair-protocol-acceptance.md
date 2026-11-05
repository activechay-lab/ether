# Continuous broadcast protocol handoff (P4)

This replaces the programme-track implementation after the owner decision at
spec 2129fdbad. No UI changes or migrations. Canonical screen JSON types landed
separately on feat/035-screens-config (377a308d7), merged by the coordinator.

## Delivered behavior

- Virtual channels use a shared continuous output. IPTV uses it when a no-signal
  screen is selected for all streams, or for a stream already being converted.
  Existing IPTV relay/passthrough and error behavior remain when screens are off.
- Shared HLS, one-Period DASH and Plex for Mac Matroska use one encoder timeline.
  HLS/DASH views use exact SegmentIndex Start/Duration, including fractional-rate
  segments. HLS emits PROGRAM-DATE-TIME and no discontinuity tags.
- Viewers retain their own signed segment URLs, access checks, monitoring and
  stream-limit reservations. DVR reads re-check every programme covered by each
  segment; broker playlists retain scoped capability paths and strip Plex auth.
  The optional offset query selects a view up to 30 minutes behind the live edge.
- HTTP attaches to the shared TS mux at any time, then copy-remuxes to Matroska
  for that viewer. Its loopback input is session-bound and unguessable. The
  media writer refreshes activity while streaming; closing a viewer closes its
  remux and loopback server, while the shared output retains its idle grace.
- Programme tracks, the three-track cap, per-programme DASH muxes, old programme
  segment routes and their implementation-specific tests are deleted.
- A cached channel ident plays once on a cold shared output, while the programme
  decoder prepares at its advanced join offset. It is not a per-viewer prefix.
- A missing interstitial is decided once per airing: gap mode restores the
  original filler/slate span; insert mode advances the next programme and corrects
  the guide on a lineup bump. Each skip logs once through faillog. Pure Schedule
  still depends only on duration data. The no-look base-code golden remains.
- Clips/screens use local master decoders with bounded queues while airing.
  Complete decoded rings were removed: a four-second 1080p I420+PCM ring would
  require 374,016,000 bytes (~356.7 MiB); 720p requires ~158.9 MiB.
- Server Screens settings use offAir/noSignal/stationId/applyTo. Per-channel
  Screens overrides use offAir/noSignal. ApplyTo defaults to converted; all opts
  into conversion. DVRMinutes is additive channel JSON, default 30 minutes.
  No core changes in P4. No HLSRequest ClipPath/ClipLoop remains.
- Shared programme resolution can use the app's owner dependency; viewer access
  is re-checked on playlists, segments and the HTTP input. W7 owns Graphics and
  Signal render/cache wiring; absent or not-yet-cached graphics use black/silence
  until the asynchronous cached screen becomes available. Screen requests follow
  the canonical screen.reason/back/channel.name/channel.number/stationId keys.

## Deliberate Plex-visible changes and changed assertions

The tune/decision response shapes are unchanged. Values describe the channel's
H.264/AAC rendition rather than the catalogue input's codec/audio layout.

- TestVirtualChannelHLSFromStartPlaylistKeepsTheSession: start-playlist URLs now
  address /relay/{seq}.ts instead of /program/{start}/{n}.ts. They remain rooted
  and signed. Session playlists retain relative relay/{seq}.ts URLs. The test's
  old per-programme encoder-seek assertion was removed; decoder scheduling is
  covered by the switcher tests. The fixture media body is now shared-ts-*.
- TestVirtualChannelDASHPlaysThroughPlexWeb: timeShiftBufferDepth changes from
  PT120S to PT1800S. The old joined/next per-programme HLSRequest assertions are
  removed. The same single Period and continuous sequence cover the boundary.
- TestVirtualDASHFailureIsAPlexDecision was removed: its HLS-only virtual backend
  no longer exists; every continuous encoder produces TS and muxed fMP4.
- Existing app Swiftfin virtual expectations change HEVC/AC3 to H.264/AAC;
  Silo/Swiftfin/Jellyfin scoped playlist and segment flows still pass. IPTV
  expectations remain unchanged. No Plex UI or response object was redesigned.

## Real three-transport continuity gate

TestBroadcastRealContinuity drives the real Go switcher and real P2 encoder
through a 20-second fake-source outage followed by recovery. The test captures
TS segments, muxed fMP4 init+media, and a late-attached Matroska HTTP stream.
Every capture decodes end-to-end with ffmpeg -xerror and no warnings. ffprobe
checks monotonic DTS and unchanged H.264/AAC/320x180 parameters. One encoder
survives the source retries, HLS has no discontinuity, and DASH has one Period.

Latest pass: 40.47 s; package 40.888 s.
Cold first finished segment: 4.460546875 s. Warm playlist: 144.667 microseconds.
The cold number misses the <=4 s target by ~0.46 s. A complete four-second
segment imposes a four-second floor plus encode/publication overhead; W5 is
measuring continuous-only low-latency encoder tuning. This is reported openly,
not rounded down to a pass. Already-running output is well inside the 1 s target.

## Whole-output resource gate

Four serial cases, each >=60 s, all niced. A real looping H.264 testsrc2+sine
file is decoded through the P2 decoder, Go switcher and one shared encoder.
One or two viewer sessions fetch the shared HLS playlist and media. Ballast
process-tree attribution includes the Go host/test harness, guardians and all
ffmpeg children; unrelated BB threads are excluded. CPU is the mean of twelve
five-second samples; RSS is the peak attributed tree memory. Sampling overhead
makes windows about 63–64 seconds. This is a synthetic capacity baseline.

| Encoder/output | Viewers | Window s | CPU cores | Peak RSS MiB |
| --- | ---: | ---: | ---: | ---: |
| Software 720p30 | 1 | 63.30 | 0.556 | 498.2 |
| Software 720p30 | 2 | 64.14 | 0.704 | 479.2 |
| VideoToolbox 1080p30 | 1 | 63.65 | 0.721 | 549.5 |
| VideoToolbox 1080p30 | 2 | 63.94 | 0.550 | 514.4 |

Both viewer counts use one decoder and encoder. The software CPU difference is
+0.148 cores; the hardware difference is -0.171, and both RSS differences are
negative. Host load, sampling and warm allocator/cache variation dominate these
deltas, so this table does not establish a precise marginal viewer cost or a
negative memory cost. It establishes that a second viewer does not add another
pipeline. Natural footage, 4K/HDR decode and different hardware need their own
capacity measurements. All four cases pass; total gate 257.23 s.

## Gates

Passed on the integration-based branch:

- nice -n 10 go test -short -count=1 -p 2 ./internal/livetv ./internal/app ./internal/plex
  (15.547 s / 40.630 s / 33.908 s)
- nice -n 10 go vet ./internal/livetv ./internal/app ./internal/plex ./internal/config
- nice -n 10 go build ./...
- CGO_ENABLED=0 nice -n 10 go build -o /tmp/ether-p4 ./cmd/ether
- nice -n 10 go test ./internal/livetv -run '^TestBroadcastRealContinuity$' -count=1 -v -timeout 75s
- nice -n 10 go test ./internal/livetv -run '^TestBroadcastResourceMeasurements$' -count=1 -v -timeout 360s -args -broadcast-measure
- git diff --check

The coordinator fixed the pre-existing console descriptor fixture drift at
integration a5ba4d7ef, which is included. The real client/device lab remains the
coordinator's release gate; this worker did not claim or alter a Plex server.
