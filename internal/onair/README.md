# On Air rendering

`scene` owns document v1, strict decoding, binding/keyframe evaluation and cost
validation. `families.Get(family, moment)` loads an embedded document;
`families.Defaults(family)` supplies example bindings. `raster.Render` is a
pure function. Supply immutable, already decoded images through
`raster.Bindings.Assets`; resolve slot values to their asset keys. There is no
network access, disk access or per-frame concurrency in the rasterizer.

`encode.Encode` writes the default 1080p30 master. `encode.WithOptions` accepts
synthesized audio, output size and a total bitrate ceiling for Signal loops.
Use `encode.Hash` with the complete profile/options to address a cached clip.
The queue, transcode slot acquisition, asset loading and cache lifecycle belong
to the integrating caller. Text uses kerning/tracking and static bundled faces;
complex shaping and CJK download are not implemented.

Reproduce the gates:

```sh
nice -n 10 go test -count=1 -p 2 ./internal/onair/...
nice -n 10 go test ./internal/onair/raster -run '^$' -bench BenchmarkRender -benchtime=1x
nice -n 10 go run ./cmd/onair-bench -out /tmp/onair-master.mp4
nice -n 10 go run ./cmd/onair-bench -family signal -moment screen.static -width 1280 -maxrate 4M -out /tmp/static.mp4
GOOS=js GOARCH=wasm go build -o /tmp/onair.wasm ./cmd/onair-wasm
GOOS=js GOARCH=wasm go test -exec "$(go env GOROOT)/lib/wasm/go_js_wasm_exec" ./internal/onair/raster -run TestParity -count=1
```

Update the PNG goldens intentionally with `UPDATE_GOLDEN=1 go test
./internal/onair/raster`. Fonts are reproducibly sourced from the revision in
`fonts/data/revision.txt` via `tools/subset-onair-fonts.py` (a Python venv with
fonttools is required). Special Elite is Apache 2.0; the other faces are OFL.

Phase 0, Apple M4 Pro: final eight-second Late night ident encoded in 12.749 s
with four workers (1.594×; Mac target 1.5× was missed). Late night ident WASM
rendered at 4.43 fps at 960×540 in host Chrome, well below the 45 fps gate.
Decision 4 uses the TypeScript Canvas2D fallback with Go golden parity in the
console (`web/src/pages/livetv/onair/scene-renderer.ts`). Reproduce its browser
gates with `tools/onair/README.md`. Cropped scratch buffers, per-job prepared
fonts/masks and a measured TinyGo spike are possible improvements; none has
been demonstrated to close the gap. The production server and Windows were not tested.
