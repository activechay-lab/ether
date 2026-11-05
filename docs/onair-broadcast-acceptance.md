# Shared broadcast engine (P3)

P3 implements the frame clock and input/encoder lifecycle in `internal/livetv`.
The canonical P2 runner types are used directly, including SegmentIndex and
AttachTS. Protocol-route wiring, deletion of programme tracks, actual media
continuity and CPU/RSS measurements belong to P4 after the P2 runner lands.

- Shared channel output registry: node-local channel key, with a viewer key
  reserved for personal rundowns. One encoder per rendition, one input plan.
- Lifetime: first acquisition starts the output; viewers attach to existing
  rendition indexes; the last viewer gets 60 seconds of idle grace. Extra
  renditions stop with their last viewer. An encoder that errors or cannot
  accept its bounded queue is dropped; leases resolve a surviving lower
  rendition. Registry/service shutdown cancels and joins all owned work.
- Fixed packed I420 working format; exact integer PCM sample accumulator for
  fractional rates. Per-rendition bounded write queues isolate encoder stalls.
- Tick-driven schedule: current input plus next input 20 seconds ahead. Prewarm
  holds one decoded frame before applying backpressure. Resolution and playback
  preparation run away from the clock. The planner uses virtualCandidates,
  known-unavailable filtering, access checks and bounded prepare attempts.
- Cached clip/screen rings have independent audio and video cursors; audio
  loops remain continuous across fractional-rate seams. Ring loads deduplicate
  and use a two-GiB decoded-cache ceiling. Every returned frame owns its PCM.
- Source stalls: hold a last picture for 1.5 seconds, then use the screen ring
  (or black/silence when none is cached). Retry closes the old decoder and
  reservation before opening the next. PCM transitions interpolate over 40 ms.
- Provider planning reuses ResolveStreamSource, grantInput, relayInput and
  source health cooldowns. Only granted loopback URLs reach the decoder; no
  provider URL/header goes onto its argv. Provider slots are taken under
  Service.mu, include existing viewers, and are released while waiting. The
  existing relay enforces tune gap and hourly limits when it opens upstream.
- The encoder request carries a 30-minute DVR window. P4 builds viewer-facing
  playlists and MPD from exact Segment.Start/Duration, including 4.004-second
  segments at 30000/1001. The engine does not invent timestamps from 4 s steps.

Lasting fake-runner tests verify continuous ticks/PCM through a 20-second stall
and recovery, one encoder lifetime, shared viewers, personal key isolation,
idle grace, 20-second prewarm and bounded first-frame hold, exact fractional
PCM counts, a 40 ms crossfade, ring audio seams and provider-slot release.
These do not claim encoded TS/fMP4 continuity; that is P4's real-runner gate.

Verification:

- `nice -n 10 go test -count=1 -p 2 ./internal/livetv` — pass (11.489 s)
- `nice -n 10 go vet ./internal/livetv` — pass
- `nice -n 10 go build ./...`
- `CGO_ENABLED=0 nice -n 10 go build -o /tmp/ether-p3 ./cmd/ether`
- `git diff --check`

No core or config changes in P3. P4 must preserve Plex for Mac's Matroska
container with a per-viewer copy-remux from SegmentIndex.AttachTS.
