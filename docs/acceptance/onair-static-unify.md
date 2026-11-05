# W12 warm static unification

Implementation: `0637cdb81`; integration merged as `5aa236a92` from `8c32f523e`.
Branch: `feat/035-static`. No lab service was started, stopped or streamed.

## Shipped behaviour

Every built-in analogue-static surface uses `signal.NewStatic` / `WarmGenerator`.
The approved 320×180 phosphor excitation is deliberate band limiting, smoothly
sampled into native 1920×1080 I420 at 30 fps. The coordinator explicitly approved
preserving that softness. The excitation advances at 15 Hz; bloom, phosphor,
scanlines and rolling bar advance at 30 Hz. There is no finite clip loop in live
static. The grey/block generator, classic/ghost variants, grainSize/noiseFps
controls and separate static-master hiss synthesizer were deleted.

Nocturne off-air composites its unchanged prepared panel and typography over the
live engine. Readiness no longer waits for a cached clip. The console uses the
same seeded warm model; native browser/Go preview mean RGB error is below 0.36
code values per channel. Designed cards keep native per-pixel film grain at
24 Hz, reduced from amount×255 to amount×24 (default .15 = 3.6 peak-to-peak).
Raster cache version changed so old grey/grain masters cannot be reused.

## Surface audit

| Path / surface | Before | After | Encoding / looping |
|---|---|---|---|
| P6 cold-tune fill / ident (`livetv/broadcast_start.go`, `broadcast_intro.go`) | Cached family ident at native output size, grain at 24 Hz; fill can replay while waiting for programme | Same designed ident, premium native grain; no separate snow generator | Finite family duration; existing bounded cold-fill policy unchanged. See each ident's measured row below |
| Signal screens (`livetv/broadcast_signal.go`, `onair/signal`) | Warm, grey classic and ghost variants; native I420 output, warm used small phosphor source | One approved warm source at native output size, continuously evolving, warm hiss | Shared continuous rendition owns bitrate; no loop. Baseline/final measurements below |
| Nocturne `late-night-offair` | 24 Hz seeded grey noise plus band/flicker/grain in finite cached document | Live warm engine with prepared fixed foreground | No cached-loop background; 1080p30 probe 3993.4 → 1658.3 kb/s |
| `signal-screen.static` scene | 15 Hz grey noise with roll/tear controls | Warm source for scene render; live engine for playout | No loop on air; 1080p30 probe 4019.5 → 1765.4 kb/s |
| Raster noise layers / scene model | Block sample controls and stateless grey generator; resolution depended on grainSize | `signal.ImageAt`, same warm source sampled at full requested resolution | Finite custom masters retain their designed document duration; no second static algorithm |
| Raster post grain (`effects.go`) | Native pixels, 24 Hz; default 38.25-code peak-to-peak excursion | Native pixels, 24 Hz; default 3.6-code excursion | Design texture repeats only with finite designed card |
| Render cache (`raster.Version`) | Cached old noise/grain at requested rendition, CRF master bitrate variable | New version invalidates all old masters; canonical static bypasses master playback | No fixed master bitrate; measured capped switcher probes below |
| Console scene renderer / `warm-static.ts` | Independent grey noise and heavy post grain | Same warm model with smooth native sampling; matching subtle grain | Browser canvas is unencoded; no bitrate. Timeline seeking deterministically replays warm state |
| Static master audio (`encode/audio.go`) | Independent harmonic hiss synthesis repeated with master | Same `signal.WarmAudio` as live engine | Live audio evolves without a clip seam; finite exported masters retain duration |
| Other Signal plates | Prepared native plate with gentle brightness/scanline drift; silent live output | Analogue static source unchanged for plates because none exists there; cached scene previews get premium grain | No noise generator; designed plates retain their live drift |

All 53 family documents are listed below: 2 analogue backgrounds, 39 film-grain
cards and 12 bug/lower-third documents with no static or grain. W12 changes no
station bugs, lower thirds, overlays, encoder settings, segmenting or transitions.
W11 changes in the integration merge are imported unchanged.

## CPU and bitrate

Measured serially with `nice -n 10` on Apple M4 Pro, software x264, 1080p30.
Bitrate ceiling is 5000 kb/s. CPU is seconds of user+system time per media second,
except the explicitly identified generator wall-time upper bound.

| Measurement | Before | After |
|---|---:|---:|
| Live static reference capture, generator upper bound + encoder CPU | 0.899 cores | 0.840 cores |
| Live static average / sliding one-second peak | 4350.5 / 4706.8 kb/s | 3028.3 / 3488.5 kb/s |
| Warm generator microbenchmark | Same approved algorithm retained | 2.59 ms/tick = .078 core at 30 Hz, zero allocations |
| Nocturne cached replay decode+encode | .575 cores | .504 cores |
| Static-screen cached replay decode+encode | .792 cores | .526 cores |
| 39 film-grain card replay mean (min–max) | .464 (.237–.688) cores | .287 (.235–.435) cores |
| Film-grain probe bitrate range | 149.5–4088.6 kb/s | 45.6–646.2 kb/s |

The live after capture passed **before the W11 integration merge**. It includes
measured encoder process CPU (.7255 cores) plus generator elapsed-time upper
bound (.1148); it excludes the rest of the Go pump. It is below §4.5's 1.5–2.2
software-core budget but does not certify total final-head channel CPU.
`live/static.json` and `live-capture.log` preserve that successful run; later failed
attempts overwrote encoder timing text, so that text must not be combined with
this JSON. Before reference metadata is the coordinator's `samples/static-b.json`.
The contended `before-benchmark.txt` is not used for the budget claim.

Cached replay numbers are actual child process CPU from serial FFmpeg decode +
software encode under the same VBV settings. They exclude the Go pump and are
**not live generator measurements**. The probe clips are silent and capped; actual
production masters use variable CRF bitrate. The wall-time ratios printed by the
offline switcher harness are decoder wait plus switcher elapsed time, not CPU.

## Evidence

All artifacts are in `v035/samples/static-unify/` under the coordinator's thread
storage. `index.html` is the playable before/after gallery; `inventory.md` and
`inventory.json` give every document's resolution, duration, temporal behaviour,
looping, bitrate and cached replay CPU. There are 82 native 1080p30 clips: before
and after for all 41 textured documents. Primary live-background after probes run
8 seconds; the remaining texture probes run 4 seconds. Designed-card probes
generally hold poster geometry while animating actual post effects, so they are
texture evidence rather than complete choreography reels. `masters/` holds the
before/after inputs; `switcher-probes.log` records all 82 successful real-switcher
captures using the deterministic offline harness.

- `nocturne-native-crops.png`: BEFORE / AFTER / APPROVED, native 640×360 crops,
  no rescaling of source pixels. All sources are native 1920×1080.
- `nocturne-offair-after.mp4`, `signal-static-after.mp4`: actual production
  decoder paths through the real switcher, live warm background.
- `live/static.mp4`: successful 20-second real-time switcher, signed shared HLS,
  copy-remux capture with audio, decode and non-frozen-frame checks.
- `console-preview.png`, `server-preview.png`, `preview-parity.txt`: native
  console/Go warm-source parity evidence.
- `cached-cpu.json`, `measure-cached-cpu.py`, `cached-cpu.log`: replay CPU matrix.

Reference provenance: visual inspection found warm approved static in
`static-a.mp4`; the supplied `continuity-demo.mp4` and `static-30s.mp4` showed the
retired grey snow. The coordinator confirmed using the warm engine and preserving
its phosphor softness. The comparison labels its actual approved source.

## Validation and remaining integration gate

Passed: `go build ./...`; `CGO_ENABLED=0 go build ./cmd/ether`; touched-package
`go vet`; focused signal/scene/raster/encode/families tests; root onair tests;
`go test -short ./internal/livetv`; web frozen install, `pnpm typecheck`,
`pnpm format:check`, and the 3 scene-renderer Vitest tests. The 82 offline capture
subtests pass. `TestBroadcastRealContinuity` passes after the integration merge
(47.76 s, cold first segment .82 s). Native preview was visually inspected.

**Release gate remains open:** after integration merge `5aa236a92`, the real-time
1080p `TestLiveSignalDemo` fails for static and Nocturne: rendition queue fills
at tick 156 / 197 with clock lag 3.87 / .54 ms, then playback returns 503.
Removing the timing wrapper reproduces static failure at tick 156 (1.42 ms lag).
See `final-live-capture.log` and `unwrapped-live-capture.log`. This has been
reported to W11/coordinator; W12 preserves their encoder/queue changes. Final-head
real-time 1080p CPU, bitrate and uninterrupted output cannot be certified until
that integration gate is resolved. The earlier successful live figures are
explicitly not a final-head pass.

An earlier non-short Live TV run also failed the large-offset remote fixture's
90-second synthetic generation deadline; a prior continuity failure was resolved
by the W11 merge as verified above. No lab was used. No internal/core or
internal/config edits. All own servers and browsers were closed.

# Complete scene texture inventory

All 53 built-in documents audited. Values below are native 1080p30 switcher texture probes under a 5000 kb/s ceiling, not uncapped master bitrate. Film-grain probes generally hold the designed card at poster time while animating its actual post effects; primary static probes animate the background. Silent visual probes. CPU replay measurements are separate from live generation and exclude the Go pump.

| Document | Before → after | Temporal / loop | Probe kb/s before → after | Cached decode+encode cores before → after |
|---|---|---|---:|---:|
| cinema-back.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3934.1 → 625.7 | 0.465 → 0.318 |
| cinema-break.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3892.1 → 612.3 | 0.458 → 0.320 |
| cinema-bug.json | no static or grain → unchanged; no static or grain | None → None Hz; not applicable | — → — | — → — |
| cinema-ident.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3905.1 → 646.2 | 0.465 → 0.317 |
| cinema-lowerthird.json | no static or grain → unchanged; no static or grain | None → None Hz; not applicable | — → — | — → — |
| cinema-offair.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3892.1 → 289.6 | 0.458 → 0.272 |
| cinema-tonight.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 4002.3 → 167.3 | 0.463 → 0.277 |
| cinema-upnext.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3942.1 → 200.4 | 0.547 → 0.320 |
| clean-back.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3965.9 → 53.6 | 0.499 → 0.310 |
| clean-break.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3937.7 → 52.0 | 0.478 → 0.258 |
| clean-bug.json | no static or grain → unchanged; no static or grain | None → None Hz; not applicable | — → — | — → — |
| clean-ident.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 4088.6 → 45.6 | 0.487 → 0.247 |
| clean-lowerthird.json | no static or grain → unchanged; no static or grain | None → None Hz; not applicable | — → — | — → — |
| clean-offair.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3993.5 → 50.1 | 0.454 → 0.265 |
| clean-tonight.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3987.7 → 112.2 | 0.463 → 0.246 |
| clean-upnext.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3978.5 → 171.4 | 0.536 → 0.272 |
| kids-back.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3944.1 → 182.0 | 0.463 → 0.287 |
| kids-break.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3957.0 → 185.4 | 0.430 → 0.300 |
| kids-bug.json | no static or grain → unchanged; no static or grain | None → None Hz; not applicable | — → — | — → — |
| kids-ident.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 4029.7 → 141.6 | 0.442 → 0.282 |
| kids-lowerthird.json | no static or grain → unchanged; no static or grain | None → None Hz; not applicable | — → — | — → — |
| kids-offair.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3942.6 → 148.2 | 0.525 → 0.309 |
| kids-tonight.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3961.6 → 313.2 | 0.508 → 0.305 |
| kids-upnext.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 4025.2 → 150.2 | 0.523 → 0.296 |
| late-night-back.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3928.8 → 63.8 | 0.586 → 0.278 |
| late-night-break.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3950.5 → 117.4 | 0.522 → 0.435 |
| late-night-bug.json | no static or grain → unchanged; no static or grain | None → None Hz; not applicable | — → — | — → — |
| late-night-ident.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 4036.7 → 138.7 | 0.688 → 0.313 |
| late-night-lowerthird.json | no static or grain → unchanged; no static or grain | None → None Hz; not applicable | — → — | — → — |
| late-night-offair.json | hard grey scene noise → approved warm live static, foreground prepared once, no loop | 24 → EMA15 + phosphor30 Hz; before: scene duration when offair looped; after: no loop | 3993.4 → 1658.3 | 0.575 → 0.504 |
| late-night-tonight.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 4033.8 → 217.2 | 0.481 → 0.293 |
| late-night-upnext.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 4053.5 → 161.0 | 0.489 → 0.281 |
| personal-back.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3884.9 → 315.7 | 0.468 → 0.323 |
| personal-break.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3986.7 → 248.2 | 0.472 → 0.282 |
| personal-bug.json | no static or grain → unchanged; no static or grain | None → None Hz; not applicable | — → — | — → — |
| personal-ident.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3997.9 → 343.5 | 0.471 → 0.311 |
| personal-lowerthird.json | no static or grain → unchanged; no static or grain | None → None Hz; not applicable | — → — | — → — |
| personal-tonight.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 4007.4 → 318.9 | 0.469 → 0.313 |
| personal-upnext.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3957.4 → 204.1 | 0.489 → 0.272 |
| personal-welcome.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 4040.0 → 308.6 | 0.484 → 0.323 |
| retro-back.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3967.4 → 278.8 | 0.453 → 0.277 |
| retro-break.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3975.3 → 222.7 | 0.456 → 0.275 |
| retro-bug.json | no static or grain → unchanged; no static or grain | None → None Hz; not applicable | — → — | — → — |
| retro-ident.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3994.8 → 275.7 | 0.475 → 0.273 |
| retro-lowerthird.json | no static or grain → unchanged; no static or grain | None → None Hz; not applicable | — → — | — → — |
| retro-offair.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3891.7 → 59.7 | 0.433 → 0.235 |
| retro-tonight.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 3910.2 → 362.9 | 0.443 → 0.286 |
| retro-upnext.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 4007.3 → 163.6 | 0.455 → 0.237 |
| signal-screen.bars.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 440.3 → 58.5 | 0.293 → 0.252 |
| signal-screen.brb.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 265.3 → 143.6 | 0.264 → 0.257 |
| signal-screen.card.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 149.5 → 108.5 | 0.237 → 0.241 |
| signal-screen.static.json | hard grey scene noise → approved warm live static, foreground prepared once, no loop | 15 → EMA15 + phosphor30 Hz; before: scene duration when offair looped; after: no loop | 4019.5 → 1765.4 | 0.792 → 0.526 |
| signal-screen.testcard.json | per-pixel film grain → native subtle film grain: amount*24 instead of amount*255 | 24 → 24 Hz; finite designed moment; offair design texture repeats with card, not analogue static | 465.8 → 106.0 | 0.308 → 0.255 |
