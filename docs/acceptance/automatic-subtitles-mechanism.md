# Automatic preferred-language subtitles: mechanism receipt

Date: 2026-11-08. **Foundation only; release feature NOT ACCEPTED.**

The backend foundation is at `4950e114d19eb75b38e733a91018ee0f48b310ff`.
The subsequent correction defaults production client capability to denied.
No production client is qualified. The configuration field remains false by
default and has no owner setting exposed. There is no placeholder, automatic
selection command, new Companion transport, browser run or physical-client proof.
The owner setting/help/status translations remain pending a supported client path.

## Primary client evidence

### Plex Web 4.163.0, build 8246fd2

Inspected the bundle served by [Plex Web](https://app.plex.tv/desktop/):
[main bundle](https://app.plex.tv/desktop/js/main-8792-0fadf59092c873ab638b-plex-4.163.0-8246fd2.js).
SHA-256:
`bdb3e4ce5d49b5eb6be48e11a4aea0f33a357561f8bf1e1303d31255f701cd97`.
Locations below are zero-based UTF-8 byte offsets in that exact minified file,
not character offsets. The identifiers are search anchors, not copied code.

| Offset | Anchor | Observed behavior |
| --- | --- | --- |
| 304821 | `refreshRemoteStreams` | Local-player export resolves to N. |
| 307510 | `function N(e){}` | That local-player refresh implementation is empty. |
| 297682 | `/player/playback/refreshStreams` | Remote Plex player manager sends the refresh command. |
| 296310 | `/player/playback/setStreams` | Remote manager can request an existing stream identifier. |
| 385932 | `ui/player/playerMedia/refreshStreams` | Internal player-media action refreshes metadata; this is not an incoming server command contract. |
| 406355 | `subtitlesDownloadActivityID` | Manual download stores the activity ID returned by its HTTP request. |
| 406744 | `n.uuid!==i.get` | Completion ignores an activity whose UUID does not match that pending download. |
| 404743 | `function C(e)` | Completion dispatches through the selected player's refresh method. |
| 3106162 | `function Op` | Remote transport uses the discovered player's connection and command IDs. |
| 3106424 | `X-Plex-Target-Client-Identifier` | Remote command targets a particular discovered client identity. |

Companion refresh followed by setStreams is a candidate **only for a verified,
authenticated receiving client**. This inspection does not establish a usable
incoming refresh route in local Plex Web, or a receiving physical device. Ether
must not advertise new Companion routes or synthesize a manual-download activity
to claim success. No existing account/server identity was changed during research.

### Jellyfin Web

Inspected commit `c1dfa7ee58b02a06605908a37227008a516d482b`, not a claimed
installed or tested release. Primary sources:
[playback manager](https://github.com/jellyfin/jellyfin-web/blob/c1dfa7ee58b02a06605908a37227008a516d482b/src/components/playback/playbackmanager.js)
and [HTML video player](https://github.com/jellyfin/jellyfin-web/blob/c1dfa7ee58b02a06605908a37227008a516d482b/src/plugins/htmlVideoPlayer/plugin.js).

The playback manager's selection method (line 1524) resolves the requested index
against the client's existing media source. Existing external text tracks can
switch without restarting the video. An unknown index does not supply a new track
definition. The HTML player's selection method (line 665) delegates to its current
track element. No general immediate VOD media-source refresh was established.
The manager's live-stream metadata refresh (near line 3821) runs on a ten-minute
interval for live stream IDs; it is not proof of a timely VOD hot-add mechanism.

`reportPlayback` (line 80) includes the current play state and event name. Local
subtitle selection updates the stored index, but emits no subtitle-specific
progress event. Ordinary progress later carries it via `sendProgressUpdate`
(line 3797). Thus:

- An omitted index is different from explicit `-1` (off).
- A changed, valid index can conservatively veto automatic work.
- An unchanged index is a routine echo, not evidence of a new manual choice.
- Deliberately choosing off when already off is unobservable in this path.
- A matching automatic-command echo cannot be attributed safely using the index
  alone. No automatic command is currently sent or acknowledged by this slice.
- These reports do not carry an Ether source revision. Server-side validation
  fences the current source; it cannot establish which generation the client
  intended if a delayed report reuses a valid index after replacement.

Consequently Jellyfin Web automation remains denied. No claimed EventName-based
manual protection, deferred placeholder, empty subtitle resource or cached 404
was introduced. Silo and physical Plex/Jellyfin/Emby clients remain unproven too.

## Additive correction boundary

`frontend.Timeline` gains optional observed audio/subtitle indices, preserving
absence. MediaBrowser forwards them only for progress, not start/stop. Raw
EventName is not a shared policy dependency. The shared broker validates session,
viewer, device, sequence and supersession before interpreting observations.

`core.PlaybackSelectionFence` and the standalone playback implementation validate
indices against the current source while holding the existing source-transition
semaphore. Lock order is source fence, then durable intent writer, then short
broker mutex. There is no provider/network work in this callback. A failed durable
veto does not advance observed options. Routine echoes cause no intent write.
An existing explicit manual command already establishes a veto; subsequent
observations update options without repeatedly writing that same veto. This is
conservative suppression, not a new persisted preference system or proof of intent.

The app's client-capability callback is nil in production. The offline fixture
injects support solely to exercise the foundation. Enabling the configuration
field therefore cannot silently turn an unsupported client's playback into a
cache-only automatic download.

## Offline evidence

No live provider, account, download quota, browser, emulator or external media was
used. The fixture serves checked-in local MP4 and fake OpenSubtitles HTTP replies.
The encoder is a fake; zero starts is not physical decoding proof.

Focused commands (GOMAXPROCS=2, serial package compilation):

```sh
GOMAXPROCS=2 go test -p 1 ./internal/app ./internal/frontends/mediabrowser ./internal/sessions -run '^(TestAutomaticSubtitleBrokerFlow|TestTimelineReportsAndStops|TestCommandAuthorizationAndTargetedDelivery)$' -count=1 -v
GOMAXPROCS=2 go test -p 1 ./internal/app ./internal/playback -run '^(TestAutomaticSubtitleBrokerFlow|TestSubtitleSourceLeaseRequiresPlayingAndFencesRetirement)$' -count=1 -v
```

The first flow exposed a fixture expectation error: its initial selected subtitle
was not off. The check now compares the unchanged actual selection. Adapter and
command checks passed in the first command; the corrected app and source-lifetime
checks passed in the second (app 3.399 s, playback 0.520 s).

Final composed receipt: source `a3ce34163a07c2b32b710b5ff673c802`, revision
`src-97eeff2566ff9f2f4850481aa3ca47a8`, appended index 4; provider searches 1,
download requests 1, encoder starts 0. Authenticated subtitle URL served the
fixture's Dutch cues. Prepare, plan, open and input read consumed zero provider
requests. Manual revision reached 2 after observed change and explicit command;
omitted fields, changed off, invalid index, foreign device, failed-DB retry and
command echo were checked. Exact/stale/stopped source fencing was checked in the
existing lifetime matrix. **Client discovery and automatic selection NOT PROVEN.**

Before any client resource window: identify a supported refresh/selection path,
provide a persistent local fake-provider fixture with exact command, port and
client version, and obtain the release coordinator's resource window. The current
fixture exits after its assertions; it is not a browser-ready server.
