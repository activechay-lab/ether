# On Air repository media (W21)

Baseline: `494ba7b9a3324c29601018e72a3eb45424308da1`.

| Committed paths | Before (bytes) | After (bytes) |
| --- | ---: | ---: |
| `internal/onair/raster/testdata` | 12,960,579 | 583,765 |
| `internal/onair/fonts/data` | 1,395,435 | 971,657 |
| Total | 14,356,014 | 1,555,422 |

Sizes are sums of file lengths, including licences, manifest and fixture README.
The new raster fixtures occupy under 1 MB, enforced by their test. All 149 PNGs
were migrated from the approved baseline, then removed from the tree. Compact
records retain exact full-render SHA-256, 32×18 RGBA, luminance dHash and a
historical blob ID for opt-in exact expected-image inspection. The format,
tolerances, update command and debug limitations are documented in
`internal/onair/raster/testdata/README.md`.

Only Archivo Black and Monoton were unused. IBM Plex Mono is used by signal
screens and Noto Sans remains the renderer fallback. The other retained fonts
are used by the built-in families. Subsets preserve Latin/Extended, combining
marks, Greek/Cyrillic and punctuation available upstream, common layout features,
and copyright/licence names. Modified primary/PostScript names are EtherAir01–11;
this respects RFNs Playfair Display, Orbitron and Plex. The tool always rebuilds
existing files from the pinned revision; use fonttools 4.68.0. Notices, server
validation and browser font-loading IDs match the reduced bundle.

The `github/main...HEAD` binary scan found one other On Air asset:
`web/src/mock/clip-preview.mp4`, 92,023 bytes. It remains unchanged and was
reported to the orchestrator. Other binary changes belong to unrelated upgrade
screenshots and playback test fixtures. No core or config changes.

Negative control: temporarily clearing the late-night ident render's pixels
caused `TestGoldenAndDeterminism/late-night-ident-3.0` to fail with mean error
0.313826, peak 255 and dHash distance 24 (limits 0.005, 32 and 6). Debug mode
recovered checksum-verified `expected.png` and wrote full-size `actual.png`.
The intentional mutation was restored immediately.

Deleting files from the current tree does not remove historical Git blobs.
Full-history clones retain the old PNGs; a shallow clone or source archive
benefits directly. No history rewrite is part of this change.

## Validation

All Go jobs ran with `nice -n 10`, `GOMAXPROCS=2`, and `-p 1`. The default
Go cache was read-only in the sandbox, so `GOCACHE=/tmp/w21-gocache` was used
and removed after the gates. Host-visible capture guards were used after
finding that sandbox PID isolation hid W11's processes.

- `go test -count=1 -p 1 ./internal/onair/...`: passed all packages.
- `go build -p 1 ./...`: passed.
- `CGO_ENABLED=0 go build -p 1 ./cmd/ether`: passed.
- `go vet -p 1 ./internal/onair/...`: passed.
- Frozen pnpm install passed using temporary pnpm 10.20.0 (host pnpm 12 was
  incompatible with the existing package.json overrides); lockfile unchanged.
- Focused `scene-renderer.test.ts` vitest: 1 file, 3 tests passed. Remaining web
  checks are delegated to the orchestrator's Mac, per its instruction.

Fresh depth-1 object stores fetched locally with `--depth=1 --no-tags`:

| `git count-objects -vH` | Integration baseline | Implementation c835b0d32 |
| --- | ---: | ---: |
| Loose objects | 0 | 0 |
| Packed objects | 6,531 | 6,528 |
| Packs | 1 | 1 |
| Pack size | 42.09 MiB | 30.12 MiB |
| Garbage | 0 | 0 |

This is an 11.97 MiB reduction for shallow fetches. A full-history comparison
was skipped: the shared source object database already contains 1.17 GiB of
packs, and both full clones would retain historical media. The measurement
above predates only these small documentation additions.
