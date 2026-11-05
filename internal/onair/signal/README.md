# Live Signal engine

Each channel/rendition owns one `Engine`. Construct it away from the wall-clock
frame path, then call `Render(video, pcm, tick)` once per30Hz tick. Video is tight
I420; audio is3200interleaved stereo int16 samples at48kHz. Caller owns both
buffers. `Render` allocates nothing and never renders text, opens files, or runs
a process. Engines contain temporal state and must not be shared across callers.

`NewStatic(width,height,seed)` is the single approved warm static. A 320×180
phosphor field contains 160×90 temporal EMA excitation and separable Gaussian
bloom. It is smoothly sampled at native output resolution with warm cream chroma,
tube falloff, faint scanlines and a slow rolling bar. It runs continuously and
never loops. There are no classic/ghost or hard-grey alternatives. Audio is
low-passed hiss with a soft 60 Hz hum and slow tuning swell.

Scene `noise` uses the same source through `ImageAt` for deterministic offline
masters and stills. Full-frame static scenes (Nocturne off-air) bypass clip loops
in playout: prepared foreground above a live engine. Console `warm-static.ts`
mirrors the phosphor model. Designed-card post grain is capped at 24 code values
peak-to-peak at amount 1 (3.6 at the built-in 0.15); it is not analogue static.

`NewPlate(width,height,style,plate,overlay,seed)` accepts decoded PNG images.
The opaque plate and transparent copy overlay are composited once and converted
to I420. Construct a replacement engine when the copy changes. Each tick applies
phosphor brightness breathing, scanline drift and a gentle tube vignette. W6's
decorative bloom is already baked into the plate; text remains sharp. All plate styles are silent.

Measured serially on Apple M4 Pro at 1080p30, niced: approved warm static
2.59 ms/tick, 0.078 core, zero allocations. See the W12 static-unify report for
complete switcher/encoder measurements and before/after evidence.

```sh
nice -n 10 go test ./internal/onair/signal -count=1 -bench BenchmarkLiveWarm1080p30 -benchmem -benchtime=2s
```

The opt-in `TestLiveSignalDemo` in `internal/livetv` uses the real broadcast
switcher and signed shared HLS. It records20s at1080p and720p per style, validates
full decode, mean luma/frame variation at2/8/14s, and one-second packet bitrate,
and emits JSON, three-time inspection strips and native crops. Captures must be
visually inspected before publishing.

```sh
nice -n 10 go test ./internal/livetv -run '^TestLiveSignalDemo$' -count=1 -v -timeout 12m -args -live-signal-demo <staging-directory> -live-signal-plates <plate-directory>
```

The shared encoder retains veryfast,CRF18,mbtree and20-frame lookahead, with
AQmode3 andpsy-rd1.0:0.15. Continuous-only VBV reserves AAC/container headroom and
limits startup bursts. Quick1080p30 synthetic programme-pattern SSIM at equal
budget:0.988152before,0.987731after. This verifies the tuning on a reproducible
fixture; it does not replace natural-footage/device validation.
