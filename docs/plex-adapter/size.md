# Prism extraction status

Snapshot on `prism/combined`: all five Prism slices plus `w0310/integration-local`. Counts include
comments and blank lines in non-test Go files, recursively within each bucket.
Nested buckets must not be added to their parents.

| Package tree | Production Go lines |
| --- | ---: |
| `internal/frontends/plex` | 39,453 |
| `internal/experience` | 14,852 |
| `internal/sessions` | 16,173 |
| `internal/hlsplay` | 865 |
| `internal/trackprefs` | 1,354 |
| `internal/livetv` | 39,535 |
| `internal/watchlist` | 3,338 |
| `internal/playback` | 16,238 |
| `internal/transcode` | 31,973 |
| `internal/artwork` | 5,197 |
| `internal/resolver` | 16,779 |
| `internal/clusterplayback` | 10,096 |

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

## Combined extraction and remaining work

The combined slices move session delivery policies into frontend adapters, HLS lab
wire control into frontend playerlab packages, guide wire handling into the Plex
adapter, and host/experience choices behind explicit policies. All additive
ClientCaps fields from the live-TV and experience slices are retained.

Prism is **not finished**. The full remaining-name inventory and justifications
are in [remaining-names.md](remaining-names.md). Shared playback still branches on
protocol for buffering reports, cluster handling and Plex part MIME/headers;
identity still branches for resolution/image grants; watch-party command selection
and state timeline handling still contain frontend behavior. Application wiring
also retains several behavior choices. These are remaining extraction work, not
justified away as identity strings. Historical migrations, account integrations,
configuration keys and conformance tooling are distinguished in the inventory.

## Verification

The recorded-reference package is `internal/conform` (formerly
`internal/pmsconform`). Plex golden, conformance, version-flow and shared-artwork
parity tests live in `internal/frontends/plex`; `.github/workflows/plex-parity.yml`
defines the wider gate. Real-device playback, resume, remembered-release
selection and poster/thumb presentation remain coordinator verification items.
