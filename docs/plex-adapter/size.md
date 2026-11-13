# Prism extraction status

Snapshot on `prism/finish`, based on `origin/integration/0310`. Counts include
comments and blank lines in non-test Go files, recursively within each bucket.
Nested buckets must not be added to their parents.

| Package tree | Production Go lines |
| --- | ---: |
| `internal/frontends/plex` | 38,789 |
| `internal/experience` | 14,947 |
| `internal/sessions` | 16,131 |
| `internal/hlsplay` | 1,003 |
| `internal/trackprefs` | 1,353 |
| `internal/livetv` | 39,742 |
| `internal/watchlist` | 3,175 |
| `internal/playback` | 16,181 |
| `internal/transcode` | 31,972 |
| `internal/artwork` | 4,919 |
| `internal/resolver` | 16,769 |
| `internal/clusterplayback` | 10,204 |

The old `internal/plex` path is gone. Plex wire rendering now lives under
`internal/frontends/plex`. This relocation does not by itself finish Prism.

## Completed in this branch

- Playback admission uses `ClientCaps.AwaitAdmissionIndex` for its bounded join
  of an already-running generation index. Plex declares the flag in its adapter.
- Remembered-release codec acceptance uses
  `ClientCaps.DeferSourceCodecValidation`; Plex declares this delivery-selection
  quirk in its adapter. Unspecified protocols retain their prior default.
- Artwork receives a landscape-thumb flag from application wiring, rather than
  identifying the frontend inside the artwork service.

## Remaining work

Prism is **not finished**. Session admission, stream bindings, surface guards,
cluster markers and persistence still contain protocol-specific behavior.
HLS laboratory playback still owns protocol request vocabulary and selection.
Track preferences, live TV guide/publication, plugin library projections,
watchlist providers, cluster playback forwarding and other playback/transcode
branches still need separation behind explicit contracts. Protocol names in
application wiring, configuration and listener administration are intentional.

Historical table names in migrations also remain. They must be distinguished
from runtime protocol decisions; renaming a table alone is not extraction.

## Verification

The recorded-reference package is `internal/conform` (formerly
`internal/pmsconform`). Plex golden, conformance, version-flow and shared-artwork
parity tests live in `internal/frontends/plex`; `.github/workflows/plex-parity.yml`
defines the wider gate. Real-device playback, resume, remembered-release
selection and poster/thumb presentation remain coordinator verification items.
