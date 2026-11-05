# Plugin content acceptance — W9, 2026-11-05

A real scratch Ether process ran at `127.0.0.1:32689`, with its own database
under `/tmp/ether-w9-data`. Plex was disabled; no account was claimed.
The example was installed unchanged, apart from owner latitude/longitude settings.

```sh
curl -H 'Content-Type: application/json' -H 'X-Ether-Request: 1' \
  --data-binary @/tmp/w9-install.json http://127.0.0.1:32689/ether/api/plugins/install
```

`w9-install.json` contains the example manifest, package files as strings, and
`settings: {latitude:43.65, longitude:-79.38}`.

```json
{
  "id": "community.example.weather",
  "name": "Weather",
  "version": "1.0.0",
  "kind": "extension",
  "summary": "Adds local forecasts to channels",
  "enabled": true,
  "verified": false,
  "shadow": false,
  "autoUpdate": false,
  "health": "ok",
  "capabilities": [
    "onair_content"
  ],
  "sourceKind": "studio",
  "installedAt": 1793898196
}
```

The host background loop fetched actual Open-Meteo data through the network
allowlist and stored `{"temp":16.8,"summary":"Current temperature"}` in
`onair_feed_values`, feed `now`, fetched_at `1793898197993` (Unix milliseconds).

```sh
curl 'http://127.0.0.1:32689/ether/api/extend/registry?kinds=onairSegment'
```

```json
{
  "version": 15,
  "descriptors": [
    {
      "kind": "onairSegment",
      "id": "plugin:community.example.weather/forecast",
      "owner": "community.example.weather",
      "label": {
        "resolved": "Local forecast"
      },
      "hint": {},
      "group": "Weather",
      "meta": {
        "duration": 120,
        "feeds": [
          "now"
        ],
        "rating": ""
      }
    }
  ]
}
```

```sh
curl -H 'Content-Type: application/json' -H 'X-Ether-Request: 1' \
  --data-binary @/tmp/w9-channel.json http://127.0.0.1:32689/ether/api/livetv/channels
curl 'http://127.0.0.1:32689/ether/api/livetv/guide?from=2026-11-05T06:00:00Z&until=2026-11-05T09:00:00Z&channels=w9-weather'
```

The channel is virtual, enabled, offAir, number 901, with this clock:
`[{"segmentId":"plugin:community.example.weather/forecast","minute":58,"everyMinutes":60,"from":360,"until":1380,"days":[]}]`.
Segment rows returned:

```json
[
  {
    "id": "w9-weather-1793861880000",
    "channelId": "w9-weather",
    "start": "2026-11-05T06:58:00Z",
    "end": "2026-11-05T07:00:00Z",
    "item": {
      "key": 0,
      "title": "Local forecast",
      "durationMs": 120000,
      "slate": "plugin:community.example.weather/forecast"
    }
  },
  {
    "id": "w9-weather-1793865480000",
    "channelId": "w9-weather",
    "start": "2026-11-05T07:58:00Z",
    "end": "2026-11-05T08:00:00Z",
    "item": {
      "key": 0,
      "title": "Local forecast",
      "durationMs": 120000,
      "slate": "plugin:community.example.weather/forecast"
    }
  },
  {
    "id": "w9-weather-1793869080000",
    "channelId": "w9-weather",
    "start": "2026-11-05T08:58:00Z",
    "end": "2026-11-05T09:00:00Z",
    "item": {
      "key": 0,
      "title": "Local forecast",
      "durationMs": 120000,
      "slate": "plugin:community.example.weather/forecast"
    }
  }
]
```

This host has no ffmpeg. Master MP4 playback is delegated to the Mac lab after
merge. Offline tests cover manifest/scene validation, typed and capped feed
outputs, leader-only fetching, last-good preservation and expiry, feed-version
hashes, fixed clock reservations, stale-slot fallback, and channel/viewer ratings.

Clock reservations bound crossing programme spans like owner calendar blocks;
the following span resumes at its original programme offset. Missing plugin
clips fill the reserved interval with filler or slate and never advance the
following schedule. Existing channels without clock/look remain unchanged.

Translation delta (new picker strings, after the copy freeze):

- `{name} · {minutes} min`
- `{name} · {plugin} · {minutes} min`
- `{name} at :{minute} every {every} minutes, {from} to {until}`

The dynamic plugin labels come from registry text and the plugin's own locale
resources. No locale catalogue was edited.

Uninstall check: `DELETE /ether/api/plugins/community.example.weather` returned
`{"status":"removed"}`; the feed table contained zero rows afterwards. The
scratch process was stopped and its listener closed.

Gates: full Go build and pure-Go Ether build passed; vet passed for plugins,
Live TV, On Air, extend, app and core. The required package suite exposed one
unchanged host-dependent failure, `TestRelayErrorsCarryNoUpstreamDetail`: its
redirect to closed `127.0.0.1:1` times out and returns 504 rather than the expected
502. A direct curl to that address times out too, both inside and outside the
sandbox. The suite passed with only that test skipped, recorded in the
handoff. Default Go cache is read-only; Go checks use `GOCACHE=/tmp/gocache`.
Web dependencies installed with `corepack pnpm@10.20.0 install --frozen-lockfile
--prefer-offline` (host pnpm 12 ignores the existing override configuration).
Web `typecheck` and `format:check` passed.

Exact Go commands (all niced, sequential, with `GOCACHE=/tmp/gocache` and
`~/sdk/go1.27.3/bin/go`):

```sh
go build -p 1 ./...
CGO_ENABLED=0 go build -p 1 -o /tmp/ether-w9 ./cmd/ether
go vet -p 1 ./internal/plugins/... ./internal/livetv/ ./internal/onair/... ./internal/extend/ ./internal/app/ ./internal/core/
go test -count=1 -p 2 ./internal/plugins/... ./internal/livetv/ ./internal/onair/... ./internal/extend/
# Above: only the unchanged host-dependent relay failure described above.
go test -count=1 -p 2 ./internal/plugins/... ./internal/livetv/ ./internal/onair/... ./internal/extend/ -skip '^TestRelayErrorsCarryNoUpstreamDetail$'
# Above: PASS, all packages.
go test -count=1 -p 2 ./internal/onair/ -run TestPluginFeedVersionAndStaleClip
go test -count=1 -p 2 ./internal/livetv/ -run 'TestPlugin|TestNoLookWeekGolden|TestInterstitialSchedule'
```

Rebase verification: W9 was rebased onto integration `28aeceb03`, retaining
personal-channel types, validation, viewer/reason/welcome bindings, the final
On Air service and continuous broadcast planning. `TestPluginClockBroadcastInputPlan`
checks both looked and clock-only channels: the scheduled plugin master MP4 is
passed to `ContinuousRunner.StartDecoder` as a non-looping graphics input, with
its scheduled boundaries intact. It also checks that personal viewer bindings
survive plugin request construction. No per-programme track path is involved.
