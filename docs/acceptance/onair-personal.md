# Personal channels acceptance (W8)

Implemented on `feat/035-personal`, rebased on P4 (`f84e2318d`).

## Delivered

- Additive `core.PersonalPick` and `core.PersonalPicker` in `core/onair.go`;
  no config changes. Personalize reuses home assembly and item reasons,
  checks effective policy, not-interested, episode release/watch state, and
  supports watchlist and explicitly selected collection keys.
- Optional Live TV picker/state/viewer-directory/watchlist dependencies wired
  in app. Recipe/policy JSON follows Appendix A. Names and metadata are scoped
  to the viewer, including Plex and XMLTV.
- Background 48-hour materialization, frozen past/current/next-30-minute
  window, ingredient shares, repeat interval, daily show share, night runtime,
  weekly block lens/genre filters, state invalidation and watchlist generation.
  Rebuilds are throttled to ten minutes; unchanged recipes refresh hourly to
  maintain the horizon. Requests read stored programmes.
- Viewer-owned cursor advances while sessions exist, persists on leave, and
  projects the guide from the cursor. Welcome intro uses viewer/resume bindings
  after more than ten minutes away. Broadcast keys, playback groups, input
  identity and lineup notifications are scoped to the viewer.

## Schema deviation

The task requested Live TV migration v5. The fetched integration already owns
v5 (`livetv_plex_guide_gate`) and v6 (channel-cache epoch), so the personal table
uses **v7**, preserving deployed history. It adds `away_ms` for restart-safe
welcome timing and registers viewer merge/move with channel/day conflict keys.
The orchestrator was notified before continuing.

## Verification

All Go commands use Go 1.27.3, `nice -n 10`, sequentially with `-p 2`.

- `go build -p 2 ./...`: PASS.
- `CGO_ENABLED=0 go build -p 2 -o /tmp/ether-w8 ./cmd/ether`: PASS.
- `go vet -p 2 ./internal/personalize ./internal/livetv ./internal/core ./internal/plex ./internal/app`: PASS.
- `go test -count=1 -p 2 ./internal/plex -run 'Live|Guide'`: PASS.
- `go test -count=1 -p 2 ./internal/livetv -run 'Personal|Broadcast|Interstitial'`: PASS.
- `go test -count=1 -p 2 ./internal/personalize ./internal/livetv ./internal/core`:
  personalize and core PASS; Live TV fails only the pre-existing
  `TestRelayErrorsCarryNoUpstreamDetail`: the redirect to `127.0.0.1:1`
  times out on this worker and produces HTTP 504 rather than the expected 502.
  Reproduced in isolation; no relay code was changed by W8.

`go test -count=1 -p 2 ./internal/personalize ./internal/livetv ./internal/core -skip '^TestRelayErrorsCarryNoUpstreamDetail$'`: PASS (all remaining tests).

Lasting tests cover viewer safety, the immutable recomposition window, two-viewer
name/programme/reason isolation and session-only cursor advancement.

## Real process route transcript

An actual Ether binary ran on port 32782 with `/tmp/ether-w8-api` as its scratch
data directory. Local owner and two local viewers were created; no external
account or Plex server was claimed. The scratch catalogue had no provider, so
released catalogue fixtures and matching stored rundown fixtures were inserted
explicitly to exercise read routing and reasons. This establishes stored-route
behavior, not playback on a physical client. Authentication values are omitted.

```text
POST /users/local => 201 {"id":2}
POST /users/local => 201 {"id":3}
GET /users => 200 {"users":[{"id":1,"plexId":0,"username":"w8owner","title":"w8owner","isOwner":true,"streamLimit":0,"maxContentRating":"","subtitleLanguages":[],"audioLanguages":[],"allowedSections":[],"lifecycle":{"source":"owner","paused":false,"plexShared":false,"canRemove":false,"canMerge":false,"canPause":false,"managedBy":""}},{"id":2,"plexId":0,"username":"Alice","title":"Alice","isOwner":false,"streamLimit":0,"maxContentRating":"","subtitleLanguages":[],"audioLanguages":[],"allowedSections":[],"lifecycle":{"source":"local","paused":false,"plexShared":false,"canRemove":true,"canMerge":true,"canPause":true,"managedBy":""}},{"id":3,"plexId":0,"username":"Bob","title":"Bob","isOwner":false,"streamLimit":0,"maxContentRating":"","subtitleLanguages":[],"audioLanguages":[],"allowedSections":[],"lifecycle":{"source":"local","paused":false,"plexShared":false,"canRemove":true,"canMerge":true,"canPause":true,"managedBy":""}}]}
POST /livetv/channels => 200 {"personal":{"ingredients":[{"kind":"picked","share":100,"enabled":true}],"pauseWhenAway":true,"nameFormat":"{viewer} TV","repeatHours":6,"maxShowShare":40,"nightMaxMinutes":60},"transcode":false,"id":"w8-personal","number":"81","name":"Personal channel","kind":"virtual","enabled":true,"favorite":false,"epoch":"1970-01-01T00:00:00Z","seed":0,"shuffle":false,"items":[]}
PUT /livetv/policies/2 => 200 {"personal":{"enabled":true,"name":"Alice TV"},"userId":2,"disabled":false,"restricted":false}
GET /livetv/policies/2 => 200 {"personal":{"enabled":true,"name":"Alice TV"},"userId":2,"disabled":false,"restricted":false}
GET /livetv/channels/w8-personal/rundown?viewer=2 => 200 []
PUT /livetv/policies/3 => 200 {"personal":{"enabled":true,"name":"Bob TV"},"userId":3,"disabled":false,"restricted":false}
GET /livetv/policies/3 => 200 {"personal":{"enabled":true,"name":"Bob TV"},"userId":3,"disabled":false,"restricted":false}
GET /livetv/channels/w8-personal/rundown?viewer=3 => 200 []

Stored fixture programmes inserted into scratch livetv_personal (empty catalogue, no provider or account claim).
GET /livetv/channels/w8-personal/rundown?viewer=2 => 200 []

GET /livetv/channels/w8-personal/rundown?viewer=3 => 200 []

Added matching released catalogue fixture records; invalid untyped keys correctly returned no programmes.
GET /livetv/channels/w8-personal/rundown?viewer=2 => 200 [{"reason":"Alice fixture reason","id":"w8-personal-1793896732200","channelId":"w8-personal","start":"2026-11-05T16:38:52.2Z","end":"2026-11-05T17:38:52.2Z","item":{"key":1000000000000002,"title":"Alice fixture programme","durationMs":3600000,"rating":"G"}}]

GET /livetv/channels/w8-personal/rundown?viewer=3 => 200 [{"reason":"Bob fixture reason","id":"w8-personal-1793896732203","channelId":"w8-personal","start":"2026-11-05T16:38:52.203Z","end":"2026-11-05T17:38:52.203Z","item":{"key":1000000000000003,"title":"Bob fixture programme","durationMs":3600000,"rating":"G"}}]

```

All started Ether processes were stopped after the exercise.
