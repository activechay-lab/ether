# v0.3.5 race sweep

Scratch branch `fix/035-races` was cut from `02c451d9ce08212bdbc0f1d06e12a0b64293bc41`, then rebased onto `042932c309` to verify Style Studio fix `0427045a52`. W24 branch `feat/035-breakpoints` was unchanged.

Linux x86_64, Go 1.27.3, `CGO_ENABLED=1`, `nice -n 10`, one package group at a time, `-race -count=1 -p 1`. Real FFmpeg/FFprobe were the shipping Jellyfin 8.1.3 tools fetched with `scripts/fetch-ffmpeg.sh linux-amd64`; verified archive SHA256 `b86dc023c64a5a9a410e6e3a938a970460214fae78f31c1553c9f88f1c289b6a`.

## Findings

The original onair run emitted 20 reports from one root cause: `Service.plan` JSON decoding reused stored `Document.Layers` and nested `Layer.Layers` backing arrays, racing a queued renderer's `collectSceneAssets` traversal. Style Studio fixed this by decoding into a fresh document. The full onair subtree passed afterward, including raster (169.502s); the parent package passed in 25.942s.

Four Live TV fixture races were fixed without changing production code:

| Shared variable | Writer | Concurrent reader | Fix |
| --- | --- | --- | --- |
| `countedBroadcastDecoder.calls` / `nextCalls` | `Next`, broadcast_test.go:299 | prewarm test assertion, broadcast_test.go:330 | Atomic counter |
| `broadcastFakeEncoder.index` | bounded-startup test, broadcast_test.go:529 | `Segments`, broadcast_test.go:41, called by addRendition worker | Existing encoder mutex guards publication and read |
| personal test's captured `now` | personal_test.go:94 | Options.Now callback at:79, called by refreshVirtualGuide | Existing synchronized testClock |
| live probe test's captured `now` | probe_live_test.go:45 | Options.Now callback at:34, called by refreshVirtualGuide | Existing synchronized testClock |

Line numbers in this table identify the failing base. Full detector output includes both access stacks and goroutine creation stacks for every one of the 24 reports in the external report below.

## Instrumentation timing

Both nonrace comparisons passed on the same box: `TestBroadcastSlowStartLargeOffsetRemote` 48.17s (26.20s recovery, 4 ranges), and `TestGuideAIMessyMeasurement` 32.36s (640 channels, 3200 guides, 256/256 first-run labelled proposals). Under instrumentation the former exceeded its 90s test context; the latter exceeded the matcher's internal 90s budget. A build-tagged test constant extends only the remote test's context by 4x and runs only the instrumented guide measurement in `synctest.Test`. Every fixture row and assertion remains. Normal measurement runs use real time. Production deadlines are unchanged. In the final race run, the remote test passed in 95.50s and the complete guide measurement passed in 538.50s. Its logged virtual elapsed time is not a performance measurement.

## Gates

- `go test -race -count=1 -p 1 -timeout 20m ./internal/onair/...`: PASS on042932c309.
- First `go test -race -v -count=1 -p 1 -timeout 20m ./internal/transcode/`: 299 passed, 7 explicit skips, no races; package timeout while starting `TestIntegrationMatroskaPresentationStartsAtSeekOffset` (1200.341s).
- Second transcode invocation uses `-race -v -count=1 -p 1 -timeout 40m -run` with the remaining 175 exact test names:171 passed, 4 explicit skips, no races; exit 0 in 149.426s. The interrupted case was restarted; every other top-level test completed once. The two batches account for all 481 top-level tests.
- `go test -race -v -count=1 -p 1 -timeout 30m ./internal/livetv/`: PASS, no races, 1171.122s (199 passed, 8 explicit skips).
- `CGO_ENABLED=0 go vet ./internal/livetv/`: PASS.
- `go test -race -list '^Test' ./internal/transcode/`: the Go-discovered 481 test names match the coverage ledger exactly; this lists tests without executing them.

## Commits and result table

- `fd3fd41fa68bd90fc1ff184232269fe9236042cb`: synchronize broadcast fixtures and mutable clocks.
- `cde06c7c900883f2e1f755b802fe53168837d176`: isolate race instrumentation from wall-clock budgets.
- Production onair fix verified: `0427045a52` (Style Studio, already in the verification base).

| Scope | Final result | Races |
| --- | --- | --- |
| onair and subpackages | PASS; parent 25.942s, raster 169.502s | 0 |
| livetv | PASS; 199 passed, 8 explicit skips, 1171.122s | 0 |
| transcode batch 1 | 299 passed, 7 explicit skips; package timeout 1200.341s | 0 |
| transcode batch 2 | PASS; 171 passed, 4 explicit skips, 149.426s | 0 |

Transcode's 11 skips are explicit hardware/platform or opt-in cases, recorded individually in the ledger. All other discovered tests completed. The initial onair run had 20 reports from one shared document bug; the initial Live TV run had four fixture races. Full logs preserve all original evidence.

## External artifacts

No recordings, images, galleries or binaries are committed. On the Mac, read `/Users/braedonsaunders/.bb/thread-storage/thr_cv85pewpp2/race-sweep/`:

- `report.md`: all 24 original detector reports, both stacks and shared variables.
- `transcode-coverage.json`: every one of the 481 test names, batch and PASS/SKIP status.
- `transcode-batch1.log` and `transcode-batch2.log`: full verbose outputs.

Original Linux logs and per-race text files are under `/data/bb/thread-storage/thr_cv85pewpp2/race-sweep/`.

No production, core, config, app, protected onair, guide_snapshot.go, or production broadcast files changed in this branch.
