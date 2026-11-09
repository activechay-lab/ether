# Native Stremio generated-poster acceptance

Status: **open**, 2026-11-08. A nonempty `poster` field and a decodable JPEG
do not establish native rendering. A client placeholder does not meet the
owner's requirement that every title have a poster.

## Evidence boundary

- The [official download page](https://www.stremio.com/downloads) lists
  Android TV 1.10.4 and Android Mobile 2.3.2 separately. Record the installed
  version in device evidence; do not transfer results between platforms.
- [Stremio's native architecture announcement](https://blog.stremio.com/stremio-tech-update-56-new-android-mobile-and-android-tv-apps-released/)
  says the newer mobile app shares the TV app's core architecture. That does
  not establish identical image-loader behavior.
- The [official Kotlin bridge](https://github.com/Stremio/stremio-core-kotlin/blob/master/stremio-core-protobuf/src/bridge/meta_preview.rs)
  forwards the poster through protobuf; the [protobuf field](https://github.com/Stremio/stremio-core-kotlin/blob/master/stremio-core-protobuf/proto/stremio/core/types/meta_item_preview.proto)
  is an optional string. These are transport paths, not a native image loader.
- Official organization repository inspection did not locate the Android
  TV/mobile UI image-loader source. Community apps with similar names are
  not substitutes for the official clients.
- `TestStremioPostersWithoutReachableAddress` proves catalog/meta serialization
  and a small 200x300 JPEG payload. Previous web evidence remains web-only.
- On bb-worker-server, `adb`, `emulator`, `jadx`, and `apktool` were absent
  from PATH; standard Android home directories were absent. This does not
  establish device availability on other hosts. Coordinator device access
  and automation approval are pending; no devices or builds were started.
- Coordinator inventory: Mac AVD definitions `plexcap` and
  `ether_xtream_acceptance` describe ARM64 Android 34 Pixel 6 devices, but
  SDK tools and system images are absent. The remaining SDK root is
  `/opt/homebrew/share/android-commandlinetools`. Definitions alone do not
  provide an executable native runtime. Do not recreate/download these AVDs
  or repeat generic SDK searches. Provisioning is an explicit prerequisite.

## Bounded device verification

After coordinator approval, use an isolated lab addon with synthetic titles
and no production account or catalogue. Exercise both catalog and meta views
on each official native app, with the actual Ether-generated JPEG data URI,
a conventional HTTPS image control, and an invalid-image negative control.
Record app version, device/runtime, payload hash/size, view, screenshot, and
image-loader errors. Repeat after cache clearing or unique synthetic IDs.
Verify the generated title text is visible, not merely a placeholder.

### Portable fixture and HTTP procedure (prepared, not executed)

`scripts/stremio-poster-fixture/main.go` uses Ether's generated-poster renderer
and writes a static addon with three synthetic titles: the actual JPEG as a
data URI, the same JPEG over HTTPS, and an invalid JPEG negative control.
The manifest exposes only catalog and meta resources, never playable media.
`evidence.json` records payload SHA256 and size and starts with native rendering
and HTTPS reachability explicitly unverified. The output directory must not
already exist. No credentials or production metadata are required.

Only after coordinator build and device approval, from the pinned source SHA:

```sh
go run ./scripts/stremio-poster-fixture \
  -out /approved/scratch/poster-proof-run01 \
  -run-id run01 \
  -https-base https://approved-lab.example/poster-proof-run01
```

The HTTPS address is a placeholder: the coordinator must supply an existing
approved lab mapping to that directory, reachable by the device. Do not deploy
or change a public proxy to satisfy this example. If a local static listener
is needed, explicitly approve its port/lifetime, bind it to loopback behind
that mapping, serve JSON/JPEG with their correct content types, and enable
`Access-Control-Allow-Origin: *` only for this synthetic fixture. Shut down
the listener at the end. No listener is started by the generator.

Before device use, fetch `manifest.json`,
`catalog/movie/ether-poster-proof.json`, all three `meta/movie/*.json` paths,
and `poster-run01.jpg` through the approved mapping. Check 200 responses,
valid JSON, matching catalog/meta IDs, a decodable 200x300 JPEG under 48KiB,
and the JPEG hash in `evidence.json`. This establishes HTTP/payload delivery
only. Install the manifest on the isolated client, inspect the catalog and
all detail views, capture each control, then remove the synthetic addon.
Use a fresh run ID per platform/repeat to avoid old poster-cache results.
Record source SHA, build command/result, runtime/app version, image hash,
HTTPS control result, data-URI result, invalid control result, screenshots,
and image-loader logs. A Pixel 6 AVD is a mobile runtime; do not call it
Android TV proof merely because a TV APK can be installed.

## Reachable fallback design if data URIs fail

Use the existing `/stremio/image/{opaque-secret}` grant route on an
owner-verified reachable HTTPS origin. Retain the viewer-scoped grant,
serve-time authorization, expiry/revocation, and image transformation. Never
put the addon credential, viewer ID, title text, or a public title key into
the URL; never publish rendered personalized artwork to an external service.
Do not derive the origin from an untrusted Host header or relax to arbitrary
plaintext LAN URLs. Keep generated image dimensions and bytes bounded.

The secure-portal path already implements this transport. No new public
deployment is required or authorized by this investigation. If the native
client rejects data URIs and no verified HTTPS address is reachable, there
is no established transport that guarantees delivery. Preserve that case
as a release blocker until a reachable authorized origin or client-supported
transport is available; do not silently accept a placeholder.

## Metadata retention integration

Reviewed batch4 `9c409b13ff64048bc2603441c61250d417947edd`:
unpersisted successful hashes enter only `q.memory`; `q.memory` reads bypass
retention touches. Only persisted hashes enter `q.cache`, whose hits can
enqueue `used_at` updates. The `loadErr == nil && persist` condition is
essential but did not cover pre-existing metadata of a title removed by tier
cleanup: loading that row or hitting its old cache entry still queued a touch.
The follow-up checks persistence only when a touch is due, with an 8ms lookup
deadline outside the cache mutex, and keeps rejected entries in memory.
Row-count assertions alone miss UPDATEs against nonexistent rows.
`TestUnpersistedArtworkNeverQueuesRetentionTouch` observes the undrained
touch queue after real background hashing and repeated reads, and uses an
old persisted row as a positive control for the retention path.
The test uses `store.OpenTest`, which honors `ETHER_TEST_POSTGRES`; it now
asserts and logs the selected dialect. PostgreSQL execution remains pending
an approved backend. Its existing helper sweeps `ether_test_*` schemas older
than two hours, so read-only preflight must find no eligible schemas before
the approved one-test command can run. Do not drop another worker's schema.
