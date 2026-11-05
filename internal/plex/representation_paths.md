# Authored Plex representation boundary

Inventory baseline: published `f4cdd85a809ec3dc8cc6beaf13c78d9cd4dd2306`.
This inventory concerns Plex VOD universal transcode URLs, not every frontend,
Live TV resource, or a claim that mixed-client specialist playback is released.

| Writer / route | Actual public URL | Existing authority |
| --- | --- | --- |
| `playback.go` HLS master | `session/{publicID}/{base-or-rung}/index.m3u8`, relative to `start.m3u8` | Account-authenticated start; child receives stream grant/read capability |
| `transcode/session_playlist.go`, `playlist.go` | `00000.ts`, `00000.m4s`, `00000.mkv`; `init.mp4` for fMP4, `header` for Matroska, relative to the child playlist | Session GET/HEAD and original owner auth |
| `transcode/dash.go`, `dash_session.go` | `session/{publicID}/$RepresentationID$/header` and `session/{publicID}/$RepresentationID$/$Number$.m4s`, relative to `start.mpd` | Session GET/HEAD and original owner auth after template expansion |
| `handlers_dash.go` | `/video/:/transcode/universal/start.mpd` | Original API start route, refuses capability-only access |
| `subtitles.go` | `/video/:/transcode/universal/subtitles` and `/subtitles/:/transcode/universal/start` | Original API routes, refuse capabilities; query/session header selection, viewer/client/title binding and selected track checks |
| `playback.go` sidecar/embedded stream handling | `/library/streams/{id}` | Original API/stream binding and subtitle policy; outside the session prefix |

`manifest.mpd` and `subtitle-{run}.ass` in `transcode/session_encoder.go`
are encoder filesystem names. They are not authored Plex URLs underneath
`/video/:/transcode/universal/session/`. Adding `.mpd` or `.ass` suffixes to
that delivery parser would invent an unsupported route, not fix a demonstrated
Ingress compatibility defect. Query-bound subtitle routes and `start.mpd`
must remain on the API routing/authentication path.

The public broker identity and private encoder registry identity are distinct.
`remotetranscode/worker.go` supplies the stable `Identity.Session` as
`ManifestSessionID`; the encoder `SessionID` is execution-specific.
The real Manager MPD writer must use the public identity. The focused Plex
fixture exercises this distinction with different registry and manifest IDs.

## Authentication and forwarding contract

Plex's strict forwarding helper accepts only GET/HEAD, an unescaped path,
a bounded request URI, and exactly three bounded safe components underneath
the session prefix. Its existing file grammar recognizes `index.m3u8`,
`init.mp4`, `header`, and `.ts`/`.m4s`/`.mkv` suffixes. It does not authorize
arbitrary files: the exact original owner handler still dispatches the actual
representation. Keep this grammar unchanged; the Runtime parser must use the
same 128-character component bound (its baseline bound is 256).

A locally accepted account credential takes precedence. A local session must
belong to that viewer. A missing entry can only trigger terminal transfer;
it cannot mint a sender entry, viewer, receipt, or authorization-cache record.
Grant-only transfer preserves the canonical stream grant and read capability,
including both together. The owner retains stream-grant-before-read-capability
priority, starting-device binding for read capabilities, started-session checks,
and fresh active-viewer resolution. Existing local failures do not delegate.

The typed private transport must validate exact owner node/boot, mTLS peer,
route generation and hop, restore the original request provenance, and invoke
the complete original Plex handler. Forwarded owner policy additionally checks
current viewer/WAN policy, title playability, source playback identity, viewer
and title binding, and current acceptance. A marker alone only adds restrictions.
Nil encoder behavior and local credential precedence are unchanged.

## Evidence limits

`TestAuthoredPlexRepresentationOwnerContract` uses real Manager HLS/MPD writers
with an in-memory process runner and real Plex manifest credential decoration.
It follows the real master child URL and checks generated URLs through the
Plex parser and complete sender/owner HTTP fixtures, including grant access,
account precedence and current title denial. Segment bytes are fixture bytes.
It is not FFmpeg, decoding, mTLS, network loss, or end-to-end specialist evidence.

Typed remote DASH and soft-subtitle methods already exist, gated by advertised
capabilities. Their existence does not prove the configured app capability set,
API placement, or playback/loss behavior. No middleware, chart, capability gate,
source-only/delivery/server guard, or production URL is changed by this work.
