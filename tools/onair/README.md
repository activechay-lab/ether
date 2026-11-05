# Scene acceptance

The console and Go read the authoritative JSON in
`internal/onair/families/documents` and the TTF files in
`internal/onair/fonts/data`. There are no copied browser assets or CDN fonts.

1. Run `UPDATE_GOLDEN=1 nice -n 10 go test -count=1 -p 2 ./internal/onair/...`
   after intentionally changing documents. This updates the 480×270 PNGs and
   their bindings manifest (which contains no duplicate scene documents).
2. Run `pnpm --dir web exec vite --mode mock --port 5193 --strictPort`.
3. Run `node tools/onair/acceptance.mjs /tmp/onair-acceptance`. It opens and
   closes its own host `playwright-cli` Chrome session. It compares 149 frames
   using weighted mean absolute luminance error (limit 0.02) and measures 30
   animated frames of every moment at 960×540. `fps` includes the
   requestAnimationFrame interval; `renderFps` is rendering throughput alone.
   The process fails if any frame exceeds tolerance or any moment is below
   45 wall-clock fps. This is a manual acceptance check, not CI.
4. Run `node tools/onair/screens.mjs /absolute/output/directory` for the dark
   1440×900 console after set and captions, including all six families and the
   screen pickers. It closes its task Chrome session.
5. Stop the Vite server you started. If the host file watcher misses changes,
   restart Vite before accepting measurements or screenshots.

The persistent jsdom test records drawing calls for all built-ins, checks
immutability and determinism, and pins evaluator boundary/colour/seed semantics.
It needs no native canvas dependency. Canvas2D antialiasing and font hinting
are permitted to differ from Go within the manual perceptual tolerance.

`onair.ServeFont` and `onair.ServeFamilies` are small resource handlers for the
On Air service to mount behind its owner authentication. The renderer requests
`/ether/api/onair/families?documents=1`; the normal family-record API remains
owned by that service. Font responses include immutable private caching and
content ETags. Their MCP exclusions are documented beside the handlers.

The worker sub-gate also measures the busiest moment of each family through
postMessage, rendering, ImageBitmap transfer and presentation. Its continuous
request loop matches the UI's latest-pending-frame handoff, without inserting an
extra RAF idle after every worker response. `workerPerformance` reports these
end-to-end delivery rates separately from main Canvas FPS.

## Owner review assets and viewer samples

`cmd/onair-samples` uses the Go rasterizer and `internal/onair/encode` for
16-second H.264/AAC masters. `-batch plates` writes static straight-RGBA PNGs
and separate transparent text overlays at 1920×1080 and1280×720; post FX are
excluded so the live engine can apply them. `manifest.json` pairs every caption.

`-batch families -width 1920` (or1280) renders ident/upnext/break for each family.
`-batch overlay -programme /path/to/1080p-programme` decodes a programme and
composites Late night bug + lowerthird over it, then encodes that master.

High-quality masters belong under `samples/masters/`. Run
`tools/onair/viewer-samples.py --root /absolute/samples --runner /tmp/onair-samples-stream`
to move completed masters there, then make the posted clips through the real
`transcode.Manager.StartDecoder` and `StartEncoder`. The tool captures four
consecutive finished TS segments through `SegmentIndex.ServeTS` and copy-remuxes
them; there is no alternate ffmpeg encode replacing the viewer pipeline.
Each JSON sidecar records dimensions, duration, average kb/s, maximum rolling
one-second packet kb/s, encoder and exact session bitrate decision. It retries
with a lower decision budget if either measured rate exceeds5000kb/s at1080p
or2000kb/s at720p. This margin accounts for the continuous encoder's VBV peaks.

Serve the samples locally on5195, then run `node tools/onair/watch.mjs <family> /review/output`
to play every file through and capture four frames per file, including browser
decode/drop telemetry. Open and review the frames before reporting the batch.
The overlay source used for review is Blender's 1920×1080 Big Buck Bunny trailer
(https://download.blender.org/peach/trailer/trailer_1080p.ogg), held outside the
repository solely as test-programme input. Signal uses live plates rather than
pre-rendered MP4 samples, per the owner's revised requirement.
