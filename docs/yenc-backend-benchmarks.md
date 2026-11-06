# Ether yEnc backend scenarios

Measured 2026-11-06, Apple M4, 10 logical CPUs, go version go1.27.3 darwin/arm64.
Decoder implementation: `1dd6edb0fe61186460c1a0ca9e67890bef5fd954`. Platform: `macOS-27.2-arm64-arm-64bit-Mach-O`.
Broad matrix: three 300ms samples per case; medians reported. Serial GOMAXPROCS=1; concurrency GOMAXPROCS=8.
Representative serial, buffer and bare-package results use five 1-second confirmation samples when present; the broad matrix uses three 300 ms samples.
Random 384/768 KiB payloads, 128/256-byte lines, CRC enabled. All backend runs were sequential.
The Ether cases include BodyReader, framing, limits and CRC; the source is an in-memory NNTP response, not a live socket.

**Units:** MB/s means decoded decimal megabytes per second. CPU time is process user + system time across all threads, including GC.
**100% busy CPU = one occupied core.** Divide by the machine's logical CPU count for whole-machine percentage.
CPU ms/GiB measures work per decoded byte; lower is better. Busy CPU alone does not measure efficiency.

**Variability:** 49 cases had a greater than 20% max/min throughput spread. This is a shared machine; interpret small differences cautiously.
Ranges below show the actual minimum–maximum samples, not confidence intervals. CPU cost is reported separately from wall throughput.

## Build combinations

| Scenario | CGO_ENABLED | GOEXPERIMENT | Selected decoder with fresh output |
|---|---:|---|---|
| Go SIMD | 0 | `simd` | RapidYenc Go (NEON) |
| C, experiment off | 1 | `(off)` | RapidYenc C (NEON) |
| SWAR | 0 | `(off)` | Ether SWAR |
| C, experiment on | 1 | `simd` | RapidYenc C (NEON) |

Caller-supplied exact/headroom buffers always select SWAR in the current integration, in every build combination.
Both cgo configurations selected the same C NEON decoder here. Their timing differences do not establish a different backend or a causal benefit from enabling the Go SIMD experiment.

## Representative CPU and throughput

768 KiB articles, 128-byte lines, fresh output; selected decoder.

| Scenario | Median MB/s | MB/s range | CPU µs/article | CPU ms/GiB | Allocations/article |
|---|---:|---:|---:|---:|---:|
| Go SIMD | 1,963 | 1,957–1,969 | 396.7 | 541.6 | 35 |
| C, experiment off | 1,885 | 1,865–1,900 | 413.4 | 564.5 | 35 |
| SWAR | 2,258 | 2,145–2,341 | 343.4 | 468.8 | 22 |
| C, experiment on | 1,799 | 1,584–1,870 | 431.7 | 589.4 | 35 |

## Fresh-output sizes and line lengths

Broad matrix (three 300 ms samples). Each cell is median MB/s and throughput ratio against forced SWAR **in the same build**. Repeated SWAR controls expose run variation.

| Article / line length | Go SIMD | C, experiment off | SWAR | C, experiment on |
|---|---:|---:|---:|---:|
| 384 KiB / 128 | 1,899 (0.87×) | 1,657 (0.75×) | 2,300 (0.98×) | 1,884 (0.84×) |
| 768 KiB / 128 | 1,644 (0.86×) | 1,211 (0.62×) | 2,224 (0.98×) | 1,718 (0.89×) |
| 384 KiB / 256 | 2,457 (1.01×) | 1,716 (0.76×) | 2,591 (1.01×) | 2,393 (0.95×) |
| 768 KiB / 256 | 2,324 (0.91×) | 1,981 (0.93×) | 2,641 (1.03×) | 2,274 (0.97×) |

## Output-buffer scenarios

768 KiB / 128-byte lines, Go SIMD build. These distinguish allocations and actual decoder selection; all four builds were measured in the raw files.

| Buffer | Actual decoder | MB/s | CPU ms/GiB | B/article | Allocations/article |
|---|---|---:|---:|---:|---:|
| fresh | RapidYenc Go (NEON) | 1,963 | 541.6 | 796,159 | 35 |
| exact | Ether SWAR | 2,456 | 437.0 | 1,088 | 21 |
| headroom | Ether SWAR | 2,396 | 445.9 | 1,088 | 21 |

`exact` has capacity equal to payload size; `headroom` adds 64 bytes. Both reuse the same caller-owned buffer on each read.

## Measured saturated concurrency: fresh output

768 KiB / 128-byte lines; fixed independent readers continuously decode. Each cell is aggregate MB/s / process CPU%.
Goroutine launch/join and runtime/GC are included; fixture/reader setup is excluded. These are not rate-limited playback measurements.

| Readers | Go SIMD | C, experiment off | SWAR | C, experiment on |
|---:|---:|---:|---:|---:|
| 1 | 2,020 / 111% | 1,452 / 112% | 2,349 / 112% | 1,890 / 109% |
| 4 | 5,670 / 390% | 4,655 / 372% | 6,737 / 363% | 5,725 / 386% |
| 8 | 7,055 / 608% | 5,301 / 541% | 8,241 / 590% | 6,240 / 573% |
| 16 | 8,444 / 675% | 6,857 / 627% | 9,264 / 660% | 5,962 / 566% |

## Measured saturated concurrency: exact output

768 KiB / 128-byte lines; fixed independent readers continuously decode. Each cell is aggregate MB/s / process CPU%.
Goroutine launch/join and runtime/GC are included; fixture/reader setup is excluded. These are not rate-limited playback measurements.

| Readers | Go SIMD | C, experiment off | SWAR | C, experiment on |
|---:|---:|---:|---:|---:|
| 1 | 2,469 / 100% | 2,037 / 99% | 2,493 / 100% | 2,120 / 100% |
| 4 | 8,244 / 377% | 5,940 / 348% | 8,591 / 368% | 6,288 / 350% |
| 8 | 10,121 / 651% | 7,976 / 571% | 11,453 / 702% | 10,203 / 670% |
| 16 | 5,985 / 439% | 9,051 / 654% | 12,359 / 754% | 11,046 / 717% |

## Bare-package diagnostic

768 KiB / 128-byte lines. Public RapidYenc decoder reused between complete responses; fresh output, or reusable output with 64 bytes of headroom.
This removes Ether's BodyReader and extra guard/metadata checks. It is a diagnostic comparison, **not a production-safe replacement**.

| Backend | Fresh MB/s | Fresh CPU ms/GiB | Reused MB/s | Reused CPU ms/GiB |
|---|---:|---:|---:|---:|
| Go SIMD | 5,605 | 186.1 | 6,686 | 160.6 |
| C, experiment off | 4,482 | 234.8 | 5,233 | 204.4 |
| C, experiment on | 4,629 | 228.3 | 5,005 | 211.9 |

## Projected decoder CPU for multiple reads

**Estimates, not rate-limited measurements.** Use serial 768 KiB / 128-byte / fresh-output CPU cost above.
Each independent, uncached reader decodes its payload once. Rates are decoded payload **Mb/s**, not MB/s or NNTP wire rates.

`CPU% = 100 × readers × (Mb/s per reader × 1e6 / 8) × CPU ns/article / article bytes / 1e9`

| Readers | Mb/s per reader | Go SIMD | C, experiment off | SWAR | C, experiment on |
|---:|---:|---:|---:|---:|---:|
| 1 | 25 | 0.16% | 0.16% | 0.14% | 0.17% |
| 4 | 25 | 0.63% | 0.66% | 0.55% | 0.69% |
| 8 | 25 | 1.26% | 1.31% | 1.09% | 1.37% |
| 16 | 25 | 2.52% | 2.63% | 2.18% | 2.74% |
| 1 | 100 | 0.63% | 0.66% | 0.55% | 0.69% |
| 4 | 100 | 2.52% | 2.63% | 2.18% | 2.74% |
| 8 | 100 | 5.04% | 5.26% | 4.37% | 5.49% |
| 16 | 100 | 10.09% | 10.51% | 8.73% | 10.98% |

At a fixed aggregate bitrate, additional readers do not automatically multiply CPU: use the total decoded rate.
These estimates exclude TLS, sockets, disk, application work and transcoding. Cache hits can avoid decoding; retries, prefetch and read amplification can add it.
Linear serial-cost projections do not capture the higher per-byte cost sometimes seen under saturated concurrency. They are useful below saturation, not an achievable-throughput guarantee.

## Reproduce and inspect

CPU metrics are implemented for macOS/Linux. A compatible C/C++ toolchain is required for cgo configurations.

```sh
bash scripts/bench_yenc_backends.sh /tmp/ether-yenc-results
```

The runner saves raw samples/metadata and calls the standard-library-only report script. Benchmarks do not change production decoder code.

- Go SIMD: [serial](yenc-backend-benchmarks/go-simd-serial.txt), [concurrent](yenc-backend-benchmarks/go-simd-concurrent.txt), [selection](yenc-backend-benchmarks/go-simd-selection.txt), [bare package](yenc-backend-benchmarks/go-simd-direct.txt), [serial confirmation](yenc-backend-benchmarks/go-simd-serial-confirmation.txt), [bare-package confirmation](yenc-backend-benchmarks/go-simd-direct-confirmation.txt).
- C, experiment off: [serial](yenc-backend-benchmarks/cgo-no-simd-serial.txt), [concurrent](yenc-backend-benchmarks/cgo-no-simd-concurrent.txt), [selection](yenc-backend-benchmarks/cgo-no-simd-selection.txt), [bare package](yenc-backend-benchmarks/cgo-no-simd-direct.txt), [serial confirmation](yenc-backend-benchmarks/cgo-no-simd-serial-confirmation.txt), [bare-package confirmation](yenc-backend-benchmarks/cgo-no-simd-direct-confirmation.txt).
- SWAR: [serial](yenc-backend-benchmarks/swar-serial.txt), [concurrent](yenc-backend-benchmarks/swar-concurrent.txt), [selection](yenc-backend-benchmarks/swar-selection.txt), [serial confirmation](yenc-backend-benchmarks/swar-serial-confirmation.txt).
- C, experiment on: [serial](yenc-backend-benchmarks/cgo-with-simd-serial.txt), [concurrent](yenc-backend-benchmarks/cgo-with-simd-concurrent.txt), [selection](yenc-backend-benchmarks/cgo-with-simd-selection.txt), [bare package](yenc-backend-benchmarks/cgo-with-simd-direct.txt), [serial confirmation](yenc-backend-benchmarks/cgo-with-simd-serial-confirmation.txt), [bare-package confirmation](yenc-backend-benchmarks/cgo-with-simd-direct-confirmation.txt).

[Buffer-reuse confirmation](yenc-backend-benchmarks/go-simd-buffer-confirmation.txt).
