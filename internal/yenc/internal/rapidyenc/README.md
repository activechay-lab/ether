# Vendored RapidYenc SIMD line kernels

Source: https://github.com/mnightingale/rapidyenc
Revision: **6657b7ca854e05728fa4a67f29ebc90ecd304428** (2026-10-08).
License: MIT, copyright Michael Nightingale; see [LICENSE](LICENSE).

`decode_arm64.go` and `decode_amd64.go` adapt the upstream
`decoder_simd_arm64.go` and `decoder_simd_amd64.go` kernels. This is a
source snapshot, not a dependency on upstream's whole-article decoder.
No C, cgo, scalar decoder, encoder or CRC implementation is included.

Local adaptations:

- Decode 64-byte blocks of an already separated, dot-unstuffed line.
- Remove NNTP terminator detection, dot unstuffing and CR/LF stripping.
  Only `=` is special; all other bytes retain Ether's DecodeLine semantics.
- Process complete blocks without upstream's protocol lookahead. Every load
  and store stays inside the provided slices, including in-place decoding.
- Return consumed bytes, produced bytes and a carried escape bit; Ether's
  unchanged SWAR decoder handles short inputs, tails and disabled SIMD.
- Guard AVX2 constant initialization and dispatch with the runtime AVX2 check.
  ARM64 uses baseline NEON. Build constraints require `GOEXPERIMENT=simd`;
  all other builds use Ether's portable SWAR implementation.

The enabled build reserves a fixed 512 KiB byte-compaction lookup table per
process. This line-kernel integration preserves per-article allocations; it
does not claim the allocation reductions of upstream's whole-article parser.

Ether retains its article parser, per-part CRC, size and wire limits, error
handling and output buffer reuse. Enable SIMD with Go 1.27.3 or newer:

```sh
CGO_ENABLED=0 GOEXPERIMENT=simd go test ./internal/yenc ./internal/usenet
CGO_ENABLED=0 GOEXPERIMENT=simd go build ./...
```

Make, Docker, GoReleaser and native installer server builds enable it.
A plain `CGO_ENABLED=0 go build ./...` remains supported and uses SWAR when
the experiment is not enabled. x86 machines without AVX2 also use SWAR.

Keep the upstream revision, local adaptations and license notice together
when updating these files. Differential tests in `internal/yenc` cover
all byte values, vector boundaries, carried escapes and overlapping buffers;
existing bad-data and limits tests apply to both paths.

## Integrated benchmark

Apple M4, Go 1.27.3, base `release/0.3.5` at `c9297236e`.
Medians of three 300 ms `BenchmarkArticle` samples with CRC enabled,
GOMAXPROCS=1, and reused output (decoded decimal MB/s):

| Article | Line length | SWAR MB/s | SIMD MB/s | Speedup |
|---|---:|---:|---:|---:|
| 384 KiB | 128 | 3,106 | 4,200 | 1.35× |
| 768 KiB | 128 | 2,878 | 4,101 | 1.42× |
| 384 KiB | 256 | 3,407 | 5,509 | 1.62× |
| 768 KiB | 256 | 3,272 | 5,222 | 1.60× |

Both paths retain the release branch's 21 allocations/article with reuse.
These numbers measure the integrated line acceleration with Ether's article
parser and verification, rather than RapidYenc's complete NNTP decoder.
They are in-memory decoding throughput, not download rates; x86 throughput
has not been measured. Reproduce from the repository root:

```sh
CGO_ENABLED=0 GOEXPERIMENT= go test ./internal/yenc -run '^$' -bench '^BenchmarkArticle$' -benchmem -benchtime=300ms -count=3 -cpu=1
CGO_ENABLED=0 GOEXPERIMENT=simd go test ./internal/yenc -run '^$' -bench '^BenchmarkArticle$' -benchmem -benchtime=300ms -count=3 -cpu=1
```
