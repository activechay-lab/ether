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
