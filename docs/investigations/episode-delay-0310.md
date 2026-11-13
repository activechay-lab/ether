# Episode release delay investigation

Base: origin/w0310/integration-local. Lab inspected read-only with SQLite
`file:~/Documents/.ether-lab/data/ether.db?mode=ro` on 2026-11-13.

## Findings

1. `catalog/convert.go` converts episode `air_date` using `tmdb.ParseDate`:
   YYYY-MM-DD becomes midnight UTC. `core.Title.IsReleased` compares this
   directly to now; there is no +24h offset. TMDB's date supplies neither
   broadcast time nor timezone. Midnight UTC is already earlier than the
   owner's Toronto calendar day, so moving this to Toronto midnight would
   delay admission another five hours. No timezone guess was introduced.
2. The general mirror defaults to a daily sync (`catalog/sync.go`), including
   airing_today/on_the_air and TMDB changes. Those lists carry show summaries,
   not current episode lists. Direct active-show and latest-season reads have
   a one-hour TTL. However `loadSeasons`, used by watchlist `Grandchildren`,
   tested the general TTL before calling the season reader, bypassing the
   one-hour policy. Fixed by sharing `seasonFresh` with direct season reads.
   This is demand-driven, with no new sweep or background API consumer.
3. Availability's 24h episode evidence window is a proof window, not a
   mandatory delay. Feed matches can open it immediately. Scheduled searches
   used offsets through +8h, then +24h. With a midnight air-date anchor, the
   next retry was at the window's excluded end: no evening retry at all.
   Followed episodes now retain hourly retries through hour 23, within the
   existing source budgets, batch limits, outage backoff and evidence window.
   Exhausted followed schedules are rearmed on the next seed pass. Unfollowed
   titles retain their existing schedule. This does not claim that an indexer
   release exists or is healthy before it actually arrives.
4. Watchlist defaults: 30-minute scheduler, six-hour normal check, one-hour
   checks for a pending episode aired within 48h. A pre-air check could suppress
   the first post-air check. That check now bypasses freshness once, then uses
   the normal bounded retry interval.
5. Plex Recently Added is constructed from catalogue browsing on each build;
   its Home cache refresh age is one minute, maximum stale age 30 minutes.
   Personalized models have a three-hour TTL. The seven-day emitted-hub
   retention preserves paging; Recently Added resolves before that fallback.
   These can add display latency, but are not a one-day episode hold.

## Real episode trace (UTC)

Law & Order S26E06, “Do No Harm”, key 4000005490260006:

| Fact | Stored value |
| --- | --- |
| TMDB-derived air date / released_ts | 2026-11-12 / 1794441600 (00:00 UTC) |
| Current metadata fetched_at | 2026-11-12 21:46:41 |
| Title check checked_at / state | 2026-11-12 21:48:15 / found |
| Title check anchor / window end | Nov 12 00:00 / Nov 13 00:00 |
| Earliest retained release first_seen | 2026-11-13 11:54:31 |
| Gate first_playable_at | 2026-11-13 11:54:31 |
| Retained release first_verified | absent (0) |

The gate opened 35h54m after the date anchor, at the first retained release
observation. `found` in the title check is earlier than that gate timestamp;
it must not be interpreted as independently verified playability. The release
rows are observed, not verified. The lab does not retain original TMDB JSON or
metadata insertion history (`added_at` is zero here); fetched_at is the most
recent fetch, not proof of first insertion. Thus these timestamps corroborate
late evidence arrival but cannot prove which external upload or historic
search first caused the user's delay. No TMDB/indexer calls were needed for
this investigation.

## Verification and remaining device checks

Coordinator should follow a currently airing show with an active season;
confirm a late-added TMDB episode appears after hourly metadata refresh and
an evening release opens discovery under the configured budget. Check Plex
Home reload and actual playback. Offline tests/build do not prove external
upload timing or client refresh behaviour. No production or lab service was
changed.
