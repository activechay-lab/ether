# yEnc SIMD validation — issue #346

Implementation branch: `perf/yenc-pure-go-simd`.
Base: `release/0.3.5` at `c9297236e827b41b5a4833af9899fb2b10ad0067`.
Recorded on 2026-11-06, Apple M4, macOS ARM64, Go 1.27.3.

## Implementation

The vendored RapidYenc NEON/AVX2 line kernels accelerate `DecodeLine`.
Ether retains its public yEnc API, framing, CRC checks, bounded decoding,
error handling and output-buffer reuse. Unsupported CPUs, architectures,
and builds without `GOEXPERIMENT=simd` use the existing SWAR decoder.
No C/cgo source or new module dependency is introduced. The enabled build
reserves a fixed 512 KiB compaction table; article allocation counts are unchanged.

Provenance, MIT license and measured article benchmark results are in
[`internal/yenc/internal/rapidyenc`](../internal/yenc/internal/rapidyenc/README.md).
The integrated article benchmark improved by 1.3–1.6× on this machine;
reused-output throughput was 4.1–5.5 GB/s, versus 2.9–3.4 GB/s with SWAR.
This is in-memory decode/CRC throughput, not network download throughput.

## Passed checks

- Existing yEnc and Usenet tests on the release base before modification.
- `CGO_ENABLED=0 GOEXPERIMENT=simd go test ./internal/yenc ./internal/usenet`.
- Portable yEnc tests with the SIMD experiment disabled.
- New SIMD dispatch, all-byte/vector-lane, carried-escape and overlapping-buffer tests.
- AMD64 yEnc suite under Rosetta with AVX2 enabled, then with
  `GODEBUG=cpu.avx2=off`; dispatch logging confirmed SIMD and SWAR respectively.
- `GOEXPERIMENT=simd go test -race ./internal/yenc/... ./internal/usenet/...`.
- `FuzzDecodeLine`: about 485,000 guided NEON cases in 15 seconds; about
  1.68 million AVX2 cases under Rosetta in 10 seconds (the standalone AMD64
  test binary ran without coverage guidance). No failures.
- `CGO_ENABLED=0 GOEXPERIMENT=simd go vet ./...`.
- `CGO_ENABLED=0 go build ./...` with and without `GOEXPERIMENT=simd`.
- Whole-module pure-Go SIMD cross-builds for Linux, macOS and Windows,
  each on AMD64 and ARM64; ARM32 yEnc package build also passed via SWAR.
- Decoder dependency graph inspection reported no cgo files.
- `goreleaser check`, shell syntax, gofmt and `git diff --check`.
- Independent code review: no actionable findings.

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
