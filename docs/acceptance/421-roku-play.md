# Roku 3.2.4 Play contract (#421)

Reference: jellyfin/jellyfin-roku tag `3.2.4`, commit
`9636e28ebd40253a1c6695ae5251f1dfc869a2a2`. Paths below are relative to
that repository; `Load` means `components/ItemGrid/LoadVideoContentTask.bs`.
No client source was copied into Ether.

The concrete server defect is negotiation: `offerJellyfinStatic` overwrote the
broker's compatibility result with `SupportsDirectPlay=true` whenever direct
play was *allowed*. Roku consequently took the static-file branch even when its
posted profile required remuxing or transcoding. A copy decision with copied
audio could also open a raw direct stream instead of the required HLS output.
Device-profile requests now follow the selected plan, use HLS for remux and
transcode decisions, and cannot silently fall back to incompatible source bytes.
This explains a bad playback offer, but does not prove the reporter's exact
BrightScript exception or decoder failure; hardware verification is required.

Roku POSTs its profile in the body and playback controls in the query
(`source/api/Items.bs:18-72`). The profile is generated from the Roku's actual
codec/container capabilities (`source/utils/deviceCapabilities.bs:27`, `:57`,
`:196`), so the server must not override its result with generic static support.

| Field / behavior | Roku reads it at | Ether before | Ether after |
| --- | --- | --- | --- |
| `MediaSources` | Load:316,333,355 | Array with source objects on success | Same; contract pins nonempty array |
| source `Id` | Load:340; source/api/Items.bs:52 | String item/version identity | Same |
| source `Protocol` | Load:439 | `File` even when an HLS offer was selected | `Http` for selected profile-based HLS; `File` for direct playback |
| source `Path` | Load:441,448,381 | Static path could win over required conversion | Scoped direct path for direct playback; absent on selected HLS |
| source/detail `Container` | Load:559 | String when known; absent on estimated detail source (client explicitly handles invalid) | Same; no guessed codec/container added |
| `SupportsDirectPlay` | Load:348,370 | Forced true despite incompatible profile | Broker decision; false for selected HLS |
| `SupportsDirectStream` | Not read by this video Play routine | Forced true when direct stream allowed | Broker decision; false for selected HLS offer |
| `SupportsTranscoding` | Not read by this video Play routine | Broker boolean | Same |
| `TranscodingUrl` | Load:358,387,404-405 | HLS URL could be bypassed by forced direct-play flag; copy/audio-copy could omit it | Used for both remux and conversion; no raw-source fallback after HLS failure |
| `TranscodingSubProtocol` | Not read by this video Play routine | String `hls` for HLS | Same |
| `IsRemote`, `VideoType` | Load:433-434 | Boolean / string | Same; local HLS cannot enter the remote-file branch |
| `MediaStreams` | Load:355,469-475,490,543 | Array, including empty when unknown | Same; test pins typed video/audio/subtitle streams |
| stream `Type`, `Codec`, `Index` | Load:355-357,420-422,472,544 | Strings / integer | Same |
| stream `Language`, `DisplayTitle`, `IsDefault`, `IsForced`, `IsExternal`, `IsTextSubtitleStream`, `DeliveryMethod`, `DeliveryUrl` | Load:654-665,490-503,543-546 | Typed stream metadata; optional delivery URL/method | Same; missing subtitle URL is guarded by the client |
| source `RunTimeTicks` | Video Play reads item ticks at Load:155,213 | Integer when known; estimated source omits unknown runtime | Same |
| `DefaultAudioStreamIndex` | No direct read in this video Play routine; selected audio index goes in query | Integer from planned choice / default when known | Same |
| `DefaultSubtitleStreamIndex` | Load:316,536 | Integer when known; absent with no subtitle streams | Same; absence is guarded by the client |
| `PlaySessionId` | Load:325 | String | Same |
| item `SeriesId`, `SeasonId` | components/data/TVEpisodeData.bs:11-12 | Nonempty GUID strings for encoded episode keys, even without ShowKey | Same across detail, Items, Episodes, Resume, NextUp |
| item `ParentIndexNumber`, `IndexNumber` | Load:168-185 | Integers | Same |
| item `UserData` | components/data/TVEpisodeData.bs:14-15; Load:281-284 | Object with boolean flags and numeric position | Same |
| `Chapters`, `People`, `Overview`, `PremiereDate`, `Trickplay` | Load:137-157 | Arrays/string; optional premiere/trickplay guarded | Same |
| `/Shows//Episodes?StartItemId=…` | Load:597-605,633-641 | 404 | Recover series from encoded episode anchor through normal authorized navigation; non-episode/missing anchor returns an empty Items array |

`SeriesId` and `SeasonId` were already correctly emitted by the shared item DTO;
no fabricated association is added to films. An empty screen-node show ID now
has a bounded response instead of an unmatched route. Recovery performs no
library search and still goes through normal viewer authorization and anchor
validation before returning episodes.

`TestRokuPlayContract` is the single new lasting test. It pins episode identity
and UserData on Items, Episodes, Resume and NextUp, detail types for a film and an
episode, profile POST response types, direct/remux/transcode selection, and the
empty-show queue behavior. It uses offline broker/library fixtures; it does not
execute BrightScript or decode media.

Coordinator must verify on Streambar 9102EU / Roku OS 16 / Jellyfin 3.2 build 4:
film and episode Play; an H.264/AAC direct source; an MKV remux; audio conversion;
1080p AVC/DDP and HEVC sources; resume and next-episode auto-play. Capture the
PlaybackInfo selected flags and ensuing static versus HLS media requests. Verify
that conversion follows the HLS URL, buffering advances, and the app stays open.
Also check an existing profile-based Jellyfin client because this negotiation
correction applies to all declared Jellyfin device profiles. The malformed-EBML
source mentioned in #421 is separate; this slice does not validate source bytes.
