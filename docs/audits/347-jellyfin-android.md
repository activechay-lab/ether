# Jellyfin Android API audit

Refs #347

Reference-only review on 2026-11-06; no upstream client implementation copied.
The inventory covers connection, login, home, video libraries, details, playback,
and the conditional calls reachable from those screens. This is source-based
coverage, not a claim of physical-device certification.

## Sources

- [Jellyfin 10.10 OpenAPI](https://api.jellyfin.org/openapi/stable/jellyfin-openapi-10.10.0.json), cross-checked against the [stable schema](https://api.jellyfin.org/openapi/jellyfin-openapi-stable.json).
- [Findroid repository implementation](https://github.com/jarnedemeulemeester/findroid/blob/013eb5253fd465d2bdfe6dc53ae87a10d77a9489/data/src/main/java/dev/jdtech/jellyfin/repository/JellyfinRepositoryImpl.kt), setup repository and SyncWorker at the same revision.
- [Android TV](https://github.com/jellyfin/jellyfin-androidtv/tree/7ada0ae8012d0d79660c4d1f0cf3c3c15d2b788b): home rows, ItemRowAdapterHelper, SdkPlaybackHelper, PlaybackManager and JellyfinMediaStreamResolver.
- [Android phone WebView](https://github.com/jellyfin/jellyfin-android/blob/6c5ab875c7ee5c2bdcc560c210d7d50466a53944/app/src/main/java/org/jellyfin/mobile/webapp/JellyfinWebViewClient.kt).
- [Jellyfin Web](https://github.com/jellyfin/jellyfin-web/tree/167433c4470ed47c472d6f19ff186aeaf6677fbf): library hooks, themeMediaPlayer and playbackmanager.

## Missing routes implemented

Paths below are relative to the frontend root. Both dedicated listeners and
`/jellyfin` or `/emby` shared-listener prefixes work. Existing user-scoped forms
remain supported; Suggestions also accepts `/Users/{userId}/Items/Suggestions`.

| Method | Path | Behavior |
|---|---|---|
| GET | /Branding/Css, /Branding/Css.css | Empty optional stylesheet, publicly accessible for login. |
| GET | /Localization/Options | Empty optional localization-provider list. |
| GET | /Items/Suggestions | Fresh viewer-authorized home recommendations, ranked and deduplicated; `userId`, repeated/CSV `type` and `mediaType`, `startIndex`, `limit`, `enableTotalRecordCount`. BaseItemDtoQueryResult, including empty Items and StartIndex. |
| GET | /Items/Filters | Visible-library genres, years and ratings; empty unsupported tag list. |
| GET | /Items/Filters2 | Visible-library genre Name/Id pairs; empty unsupported language/tag lists. |
| GET | /Shows/Upcoming | Authorized episodes from yesterday UTC onward, ordered by release, with paging. |
| GET | /Videos/{id}/AdditionalParts | Empty query result after authorizing the item; sources are complete editions. |
| GET | /Items/{id}/ThemeMedia | Authorized nested empty theme-song/video/soundtrack results. |
| GET | /Items/{id}/ThemeSongs, /Items/{id}/ThemeVideos | Authorized empty ThemeMediaResult. |
| GET | /Items/{id}/PlaybackInfo | Reuses playback negotiation; GET defaults to informational estimate, POST retains playback behavior. |
| DELETE | /Videos/ActiveEncodings | Stops only the broker session owned by the authenticated viewer/device; rejects a conflicting deviceId. |
| GET | /Artists, /Artists/AlbumArtists | Empty query results; this surface advertises video libraries. |
| GET | /Items/{id}/InstantMix | Authorized empty query result; no music index. |
| GET | /LiveTv/Timers, /LiveTv/SeriesTimers, /LiveTv/Recordings/Folders | Empty query results; no DVR scheduler is advertised. |

Optional collections use explicit routes, not a wildcard that masks missing
items or unsupported mutations. Item-dependent requests continue to return 404
for inaccessible or nonexistent items, as Jellyfin does. Authentication and
userId scoping run before reads. Suggestions uses Experience's current
viewer-policy/safety projection rather than a frontend recommendation cache.

## Existing working API sequence

| Phase | Requests already mounted |
|---|---|
| Discovery/login | GET System/Info/Public, System/Endpoint, System/Ping, Branding/Configuration, Users/Public, QuickConnect/Enabled, QuickConnect/Connect; POST Users/AuthenticateByName, Users/AuthenticateWithQuickConnect, QuickConnect/Initiate, QuickConnect/Authorize |
| Startup/home | GET Users/Me, Users/{userId}, UserViews, Users/{userId}/Views, DisplayPreferences/{id}, UserItems/Resume, Users/{userId}/Items/Resume, Items/Latest, Users/{userId}/Items/Latest, Shows/NextUp, Sessions; POST Sessions/Capabilities, Sessions/Capabilities/Full, DisplayPreferences/{id}; GET socket (WebSocket upgrade) |
| Browse/search | GET Items, Users/{userId}/Items, Items/Root, Genres, Studios, Persons, Search/Hints, Playlists/{id}/Items; movie recommendations; library view IDs and item IDs resolve through the existing viewer scope |
| Detail | GET Items/{id}, Users/{userId}/Items/{id}, Shows/{id}/Seasons, Shows/{id}/Episodes, Items/{id}/Ancestors, Items/{id}/Similar, Items/{id}/LocalTrailers, Items/{id}/SpecialFeatures, Items/{id}/Intros; item image variants |
| Playback | POST Items/{id}/PlaybackInfo, LiveStreams/Open, LiveStreams/Close; GET Videos/{id}/stream, stream containers, master.m3u8 and broker-issued media URLs; subtitle routes; MediaSegments/{id}; Trickplay tiles; POST Sessions/Playing, Sessions/Playing/Progress, Sessions/Playing/Stopped |
| State | POST/DELETE UserFavoriteItems/{id}, UserPlayedItems/{id}, user-scoped equivalents; POST Users/{userId}/Configuration; POST Sessions/Logout |
| Conditional live TV | GET LiveTv/Info, Channels, Channels/{id}, Programs, Programs/{id}, Programs/Recommended, Recordings |

## Remaining gaps outside the online video sequence

These were found in conditional settings/offline/diagnostic paths and are **not
implemented** here:

- Findroid POST Devices/Options (custom device name) and GET/POST
  UserItems/{id}/UserData (offline state reconciliation; playback progress uses
  the existing session endpoints). The current Experience contract lacks a
  general offline progress mutation.
- Android TV POST ClientLog/Document (diagnostic upload), DVR timer create,
  update, delete and per-timer detail/default routes. Ether has no DVR scheduler.
- Android TV DELETE Items/{id} (server file deletion). Ether does not advertise
  item deletion permission.
- Music audio streams/lyrics: no music views are advertised. Missing artwork,
  user portraits, splash screens and lyrics may correctly return 404 when no
  such asset exists; they must not be replaced by JSON pretending to be media.

**Official Android phone limitation:** its WebView loads the server's Jellyfin
Web bundle, then injects its native player integration. Ether's `/web` routes
serve a connection-information page, including for JavaScript asset requests.
Consequently that app cannot load its home UI from Ether even when every API
route succeeds. Supplying a real Web bundle and its native integration is still
required; API replay does not establish official Android phone support.

## Verification

`TestMediaBrowserRealExperience` now replays Findroid discovery/login and
startup/home through the mounted frontend with real Ether authentication and
Experience, for both prefixes. It requires HTTP 200 and valid JSON for every
request, including Suggestions, resume, next-up, views and latest. It also
checks the added optional browse/detail routes. Focused Suggestions tests cover
rank, deduplication, filtering, offsets, zero limit, disabled totals, malformed
pagination and cross-viewer rejection. Playback tests cover GET without opening
a session; encoder-stop tests verify viewer/device scope.
