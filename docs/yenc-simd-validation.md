# yEnc SIMD validation — issue #346

Implementation branch: `perf/yenc-pure-go-simd`.
Base: `release/0.3.5` at `c9297236e827b41b5a4833af9899fb2b10ad0067`.
Recorded on 2026-11-06, Apple M4, macOS ARM64, Go 1.27.3.

## Implementation

Ether directly imports `github.com/mnightingale/rapidyenc`, pinned to
`v0.0.0-20261008131718-6657b7ca854e`, and uses its public `NewDecoder`/`Next`
API. There are no copied upstream decoding kernels. The MIT notice is in
`THIRD_PARTY_NOTICES.md`.

The adapter preserves Ether's `DecodeLines` API, header/trailer metadata,
CRC checks (including SkipCRC), partial-data errors, line and wire limits,
decoded-size limits and caller-buffer reuse. It bounds and normalizes input
before forwarding it as synthetic NNTP framing. It stops the original reader
at the real `=yend` boundary. Only fully consumed synthetic responses return
to the decoder pool; input/output references are cleared before pooling.
Malformed size hints resume bounded SWAR decoding rather than allowing
upstream to grow an output buffer without a limit. Rare noncanonical lines
are normalized through the existing line decoder to retain Ether semantics.
The architecture-specific helpers only count/validate bytes for these bounds;
RapidYenc performs normal article decoding and CRC calculation.

The public upstream decoder requires at least 1 KiB and 64 bytes of output
headroom. Small/unknown sizes and existing caller buffers without that
headroom use SWAR. `DecodeLine` remains SWAR. Unsupported CPUs/architectures,
builds without `GOEXPERIMENT=simd`, and cgo-enabled builds use SWAR. The last
rule prevents upstream's C backend from being selected. Release builds set
`CGO_ENABLED=0 GOEXPERIMENT=simd`.

## Integrated article benchmarks

Median of three 300 ms runs, one benchmark CPU, decoded MB/s (decimal).
Random payloads, full article framing and CRC verification are included.
These are in-memory decoder rates, not network download speeds or measured
whole-application CPU usage. Runs were sequential without concurrent tests.

| Reused buffer with 64-byte headroom | SWAR MB/s | RapidYenc MB/s | Speedup |
|---|---:|---:|---:|
| 384 KiB, 128-byte lines | 2,832 | 3,416 | 1.21× |
| 768 KiB, 128-byte lines | 2,446 | 3,339 | 1.37× |
| 384 KiB, 256-byte lines | 3,043 | 4,346 | 1.43× |
| 768 KiB, 256-byte lines | 2,774 | 4,339 | 1.56× |

Fresh-output cases measured 2,558–3,564 MB/s versus SWAR's 2,155–2,793 MB/s
(1.12–1.40× paired speedups). Exact-capacity caller buffers intentionally use
the same SWAR path and have no claimed SIMD improvement.

The direct public API adds allocation overhead: fresh output uses 31
allocations/article versus 22 for SWAR; reused output with headroom uses 30
versus 21. The latter allocates 1,288 versus 1,016 bytes/article. Fresh output
allocates approximately 8 KiB more due to allocation size-class rounding for
the extra headroom. Exact-capacity reuse remains at 21 allocations/article.
These integrated results do not imply the larger gains from standalone
kernel/package benchmarks.

Reproduce the paired benchmarks:

```sh
CGO_ENABLED=0 GOEXPERIMENT=simd go test ./internal/yenc -run '^$' -bench '^BenchmarkArticle' -benchmem -benchtime=300ms -count=3 -cpu=1
CGO_ENABLED=0 GOEXPERIMENT= go test ./internal/yenc -run '^$' -bench '^BenchmarkArticle' -benchmem -benchtime=300ms -count=3 -cpu=1
```

## Passed checks

- `CGO_ENABLED=0 GOEXPERIMENT=simd go test ./internal/yenc/... ./internal/usenet/... ./internal/stream/...`.
- Portable yEnc tests with the SIMD experiment disabled.
- Public-decoder dispatch, metadata/CRC, malformed size hints, truncated data,
  split escapes, output buffer identity, sequential/concurrent decoding,
  line-count validation, 300 deterministic mutated article comparisons against
  SWAR, and trailer control-character/pool isolation regression tests.
- `FuzzRapidArticleCompatibility` ran for 15 seconds without a failure; the
  run recorded only seven executions, so this is limited fuzz coverage.
- AMD64 yEnc suite under Rosetta with AVX2 enabled, then with
  `GODEBUG=cpu.avx2=off`; public-kernel dispatch/fallback verified.
- `GOEXPERIMENT=simd go test -race ./internal/yenc/... ./internal/usenet/...`.
  Race builds enable cgo and therefore test the SWAR path, not the SIMD adapter.
  The SIMD adapter's concurrent decoding test runs separately with cgo disabled.
- `CGO_ENABLED=0 GOEXPERIMENT=simd go vet ./...`.
- `CGO_ENABLED=0 go build ./...` with and without `GOEXPERIMENT=simd`.
- Whole-module pure-Go SIMD cross-builds for Linux, macOS and Windows,
  each on AMD64 and ARM64; ARM32 yEnc package build also passed via SWAR.
- Dependency graph includes the pinned upstream package in pure-Go SIMD
  builds and reports no cgo files; cgo-enabled builds exclude that package.
- Release configuration check, shell syntax, gofmt and `git diff --check`.
- Independent code review: the pooled-input isolation finding was reproduced,
  fixed by serializing parsed trailer fields, and regression-tested; no
  remaining actionable findings.

## Full-suite failures

`CGO_ENABLED=0 GOEXPERIMENT=simd go test -timeout 10m ./...` completed with
these seven failures in five packages. **Every test below also failed in a
separate run with SIMD disabled** (`CGO_ENABLED=0 GOEXPERIMENT=`).
The failures are outside the changed yEnc path and have not been changed here.

| Package | Test | Observed failure |
|---|---|---|
| `cmd/plexparity` | `TestInventoryReportsUncoveredRoutes` | Existing Plex GET routes absent from coverage inventory |
| `cmd/plexparity` | `TestSeed` | Empty live-TV fixture guide |
| `internal/frontend` | `TestFrontendImports` | `frontends/mediabrowser/client_compatibility.go` imports forbidden `internal/core` |
| `internal/spike` | `TestPlexIntegration` | Playback timeline state has zero offset/duration |
| `internal/state/prismcompat` | `TestPrismStateAndPlexwatchSchema/sqlite_from_state2` | Migration `state/8`: missing `state_history` table |
| `internal/transcode` | `TestIntegrationMatroskaContractPillarboxesTenBitSDR` | Rendered YCbCr pixel values differ from expected values |
| `internal/transcode` | `TestIntegrationBurnedTextSubtitleRenders` | Subtitle cue not detected in rendered frame |

The full suite is therefore **not green**, although the affected decoder and
Usenet tests, race checks, builds and vet pass. Windows installer execution,
Docker image assembly and native installer signing were not performed;
release configuration and cross-builds were checked instead.
