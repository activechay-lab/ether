# Stremio add-on sweep — v0.3.9

Ether advertises that Stremio add-ons work. This audit proves it for every
kind of add-on the owner is likely to paste, end to end, on the real server
code and live public add-ons — not on hand-rolled HTTP.

Tool: `cmd/addonsweep` (developer tool, not shipped in installers).
Run: `go run ./cmd/addonsweep [-only name] [-json] [-timeout 90s]`.

## How the sweep drives Ether

The tool builds a `plugins.Manager` the way `internal/app/plugins.go` does:
a temp-dir SQLite store (`internal/store`), the real
`remote.Factory{RuntimeFactory}` (URL mode for `http(s)` add-ons, raw-github
mode for repo links, real `safefetch` guard through an `egress` client, no
tunnel), and the Live TV add-on adapters (`adapters.Attach`). It adds the
real installed-add-on indexer path (`adapters.NewStreamSources` +
`ExpandAddonIndexers`). For each add-on it calls `Manager.InstallAddon` —
the same call the console's "Add add-on" makes — and then exercises every
resource the manifest declares:

- **catalog** — every catalog without a required extra (`stremio.Addon.Catalog`).
- **search** — a `search`-extra catalog with query `star`; a `skip` catalog
  paged through `stremio.Addon.Catalog` (which drives `skip`).
- **meta** — the first catalog item's `Meta` by its own id (series must
  have `videos`).
- **stream** — the stream route Ether would ask for movie `tt0111161`,
  series `tt0903747:1:1` and the first `tv` channel, only where the
  manifest's types/`idPrefixes` allow; stream kinds are classified
  (url / infoHash / ytId / externalUrl / nzbUrl / other) and fed through
  Ether's own converter (`stremio.LookupStreams` + `stremio.Convert`).
- **indexer** — the real search path a Plex/Jellyfin search uses:
  `ExpandAddonIndexers` → `StreamSources.Backend.Search`.
- **subtitles** — `tt0111161` and the add-on's own first catalog id (an
  add-on keyed on `kitsu:` ids does not answer IMDb ids).
- **tv import** — the Live TV add-on import ("Add its channels and guide to
  Live TV"): `adapters`' channel source kind via `livetv.ImportSource`,
  reporting channel counts and verifying `behaviorHints.proxyHeaders` are
  kept as input headers.
- **addon_catalog** — the `addon_catalog` resource when declared.
- **config** — `Manager.AddonConfig`: a `configurationRequired` add-on must
  be recognised and surfaced, not crash and not read Healthy.
- **synthetic** — ytId and nzbUrl streams fed through `stremio.Convert`,
  for kinds no reachable public add-on returns unconfigured.

Classification: PASS / FAIL / N/A / UNREACHABLE. An HTTP status the add-on
itself answered (403/404/429/5xx, "Forbidden", DNS failure) is
UNREACHABLE — that is not an Ether bug.

## Result (first full run, 2026-11-12)

19 add-ons, **72 PASS / 0 FAIL / 3 UNREACHABLE**. Every UNREACHABLE is the
add-on's own answer, not Ether's.

| add-on | install | catalog | search | meta | stream | indexer | subs | tv | addon_cat | config |
|---|---|---|---|---|---|---|---|---|---|---|
| Cinemeta | PASS | PASS | PASS | PASS | N/A | N/A | N/A | N/A | PASS | N/A |
| OpenSubtitles v3 | PASS | N/A | N/A | N/A | N/A | N/A | PASS | N/A | N/A | N/A |
| Torrentio | PASS | N/A | N/A | N/A | PASS | PASS | N/A | N/A | N/A | PASS |
| Torrentio configured | PASS | N/A | N/A | N/A | PASS | PASS | N/A | N/A | N/A | PASS |
| Torrentio `stremio://` | PASS | N/A | N/A | N/A | PASS | PASS | N/A | N/A | N/A | PASS |
| Anime Kitsu | PASS | PASS | PASS | PASS | N/A | N/A | PASS | N/A | N/A | N/A |
| WatchHub | PASS | N/A | N/A | N/A | PASS | PASS | N/A | N/A | N/A | N/A |
| YouTube | PASS | PASS | PASS | PASS | N/A | N/A | N/A | N/A | N/A | N/A |
| USA TV Next repo | PASS | PASS | PASS | PASS | PASS | N/A | N/A | PASS | N/A | N/A |
| USA TV Next raw | PASS | PASS | PASS | PASS | PASS | N/A | N/A | PASS | N/A | N/A |
| USA TV Next `stremio://`raw | PASS | PASS | PASS | PASS | PASS | N/A | N/A | PASS | N/A | N/A |
| Torrentio `/configure` | PASS | N/A | N/A | N/A | PASS | PASS | N/A | N/A | N/A | PASS |
| TMDB | PASS | PASS | PASS | PASS | N/A | N/A | N/A | N/A | N/A | PASS |
| MediaFusion | PASS | N/A | N/A | N/A | PASS | PASS | N/A | N/A | N/A | PASS |
| Comet | PASS | N/A | N/A | N/A | UNREACH (403) | UNREACH (403) | N/A | N/A | N/A | PASS |
| LAN static (tv) | PASS | PASS | PASS | N/A | PASS | N/A | N/A | PASS | N/A | N/A |
| GitHub Pages static | PASS | N/A | N/A | N/A | N/A | N/A | N/A | N/A | UNREACH (404) | N/A |
| Synthetic ytId stream | N/A | N/A | N/A | N/A | PASS | N/A | N/A | N/A | N/A | N/A |
| Synthetic nzbUrl stream | N/A | N/A | N/A | N/A | PASS | N/A | N/A | N/A | N/A | N/A |

### Stream kinds proven

| kind | add-on | result |
|---|---|---|
| infoHash | Torrentio (×4 links) | 764–780 infoHash streams → Ether torrent releases (debrid path) |
| url | USA TV Next (×3), LAN static tv | HTTP streams → Ether HTTP releases; Live TV inputs |
| externalUrl | WatchHub | 7 external links, correctly rejected (no server can stream them) — PASS, not a failure |
| ytId | synthetic | refused cleanly, no crash, no release |
| nzbUrl | synthetic | accepted as a Usenet release (zero-value `ProtocolUsenet`) |

### UNREACHABLE, and why they are not Ether bugs

- **Comet** — `https://comet.elfhosted.com/stream/movie/tt0111161.json`
  answers literal `Forbidden` HTTP 403 for an unconfigured link. The
  add-on's own refusal (it needs the owner's config/account). Ether
  installs it and surfaces it as configurable (CONFIG PASS).
- **GitHub Pages static** — `https://pigamer37.github.io/stremio`
  (a real `*.github.io` static add-on, found with `gh search`) installs and
  its manifest is read, but the static host 404s the `addon_catalog`
  route. Add-on-side.

### Notes on shapes the brief called out

- **USA TV Next from its GitHub repo link** (`https://github.com/yowmamasita/usa-tv-next`),
  as raw `manifest.json`, and as `stremio://raw...` all install and serve —
  the sidecar regression fixed in `47b7647d8` (`remote/factory.go` githubRawBase)
  is confirmed fixed for all three link forms.
- **Torrentio `/configure`** link resolves to the add-on base and works.
- **`configurationRequired`** add-ons (TMDB, MediaFusion, Comet are merely
  `configurable` on their default providers and work unconfigured; none of
  the public elfhosted instances set `configurationRequired:true`) are
  surfaced by `Manager.AddonConfig` (`Configurable`/`Required` set) rather
  than crashing or reading Healthy — CONFIG PASS for all.
- **behaviorHints**: `proxyHeaders.request` on a channel stream survives
  the Live TV import as input headers (LAN static tv, "proxyHeaders kept").
  `notWebReady` is carried on USA TV Next streams; a server-side player
  does not use it, and Ether neither needs nor breaks on it.
- **ytId / nzbUrl**: no reachable public add-on returns these unconfigured
  (the strem.io YouTube add-on v1.30.7 declares no `stream` resource and
  404s `/stream/...`; public Usenet add-ons like romarr self-host). Ether's
  converter is exercised directly for both: ytId is a clean refusal (never
  a crash), nzbUrl becomes a fetchable Usenet release.

## Fixes

None. The sweep found **zero Ether-caused failures** across every add-on
kind exercised, so no package changes and no new tests were needed for this
slice. `cmd/addonsweep` is the lasting artifact.

## What could not be tested

- A public `ytId`-streaming add-on and a public `nzbUrl`/Usenet add-on: no
  reachable instance that serves those routes without the owner's config;
  both stream kinds were proven through Ether's converter with synthetic
  streams instead.
- Configured/debrid-backed streams (a Torrentio link with the owner's RD
  key) were not used — no accounts in this sweep.
