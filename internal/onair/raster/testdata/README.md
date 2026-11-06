Raster golden format
====================

Each case stores a 32×18 area-filtered premultiplied RGBA thumbnail (base64 in
JSON), a 64-bit luminance difference hash, dimensions, and SHA-256 of the full
480×270 render's RGBA bytes. `manifest.json` retains the family/moment/default
bindings index. All fixtures together must remain below 1 MB.

The full checksum takes the exact-match fast path. Otherwise all three limits
must pass: normalized RGBA mean error ≤ 0.005, maximum thumbnail channel error
≤ 32/255, and hash Hamming distance ≤ 6/64. Alpha participates, so transparent
bugs and lower thirds are covered. The mean allowance preserves the old
full-image golden test's tolerance; area filtering avoids aliasing small text
onto a few thumbnail samples. Very small differences can pass intentionally;
this is a perceptual comparison, not a pixel identity requirement.

Run `go test ./internal/onair/raster -run TestGoldenAndDeterminism`. Set
`UPDATE_GOLDEN=1` only when approving a visual change; review the compact diffs
and inspect a full render before committing. That updates the records and
manifest directly, without saving full PNGs in the repository.

Set `ONAIR_GOLDEN_DEBUG=1` on a failing run to write inspection images in an
`onair-golden-*` directory under the OS temporary directory (path in test log).
`actual.png` is full resolution. Migrated records include the historical PNG
blob ID: when that object exists in the local Git history, `expected.png` is
recovered and its full pixel checksum verified. In shallow clones, source
archives, or newly updated records without a historical PNG, the expected
image is instead a full-resolution enlargement of the compact thumbnail,
explicitly named `expected-thumbnail-enlarged.png`. Fine detail cannot be
reconstructed from the thumbnail. Normal tests never invoke Git or write PNGs.

The initial migration is from integration commit
494ba7b9a3324c29601018e72a3eb45424308da1, including its approved bug and static
fixtures. No full-size PNG remains in the tree. Old blobs remain in Git history;
removing them from historical commits would require a separate history rewrite.
