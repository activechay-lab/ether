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

## Bounded device verification

After coordinator approval, use an isolated lab addon with synthetic titles
and no production account or catalogue. Exercise both catalog and meta views
on each official native app, with the actual Ether-generated JPEG data URI,
a conventional HTTPS image control, and an invalid-image negative control.
Record app version, device/runtime, payload hash/size, view, screenshot, and
image-loader errors. Repeat after cache clearing or unique synthetic IDs.
Verify the generated title text is visible, not merely a placeholder.

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
