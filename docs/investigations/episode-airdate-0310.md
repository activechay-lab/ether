# Streaming-first episode visibility

Built on w0310/episode-delay (8ad17ac03). Coordinator confirmed TMDB TV
259909 season 2 lists November 1/8/15/22, 2026; November 13's streaming
release therefore precedes the TMDB date by two days. No second TMDB lookup
or production access was needed.

## Date sources and gates

`episodeToTitle` uses TMDB episode `air_date`, parsed as midnight UTC.
Catalogue Children/Grandchildren, cached visibility, SQL episode counts and
next-up consumers reject future `Released` values before availability can
help. The availability due-title search and background verification also
previously rejected future episodes. Moving UTC midnight to local midnight
would make this case worse and cannot fix a two-day metadata discrepancy.

TVDB is an external identifier here, not an episode-date provider. Trakt
first_aired is not consumed by this catalogue conversion. TMDB networks are
network identities, not per-platform streaming dates. Episode groups are
fetched on demand for alternate ordering; their embedded episodes expose the
same single air_date field and are not a separate streaming schedule feed.
No additional provider calls, episode-group sweep or show/network exceptions
were introduced.

## Change

The existing bounded monitor seeds the next dated regular episode of followed
shows up to 72h early, with six-hour pre-air retries. After its official date,
followed episodes retain the episode-delay slice's hourly first-day retries.
Follow signals include watchlist entries and the latest 200 watch-state rows
from the last 30 days (a covering partial recency index). There is no new
worker, timer, library scan or request-path search. Existing source budgets,
10-title batches, provider checks and playback pauses remain in force.

A mere pre-air search match does not prove playability: it is retained for
existing health/cache verification. Verified torrent cache evidence, successful
background health checks and actual playback observations can open the gate
before the metadata date. Gate opening records an earliest effective date in
a small catalogue evidence table, for the episode and its season container.
The catalogue uses the earlier of metadata and this evidence; subsequent
metadata refreshes cannot hide an already playable early release. This lets
existing date predicates work consistently across catalogue lists, Plex season
children and next-up without a per-request join or separate frontend exception.
Evidence does not promise a source will remain healthy forever; playback still
validates sources as before. The effective release date displayed can become
the first observed playable timestamp when that precedes TMDB's date.

## Validation

CGO_ENABLED=0 go build ./... passed. Focused tests cover persistence across
metadata refresh, an earlier metadata date winning over evidence, season
navigation admission, and bounded pre-air versus unfollowed retry cadence.
Touched-package vet is recorded in the worker delivery.

Coordinator must verify a followed streaming-first show's early release on
real Plex clients: configured monitoring and usable sources, first pre-air
search, health/cache verification, season children, On Deck/next-up, Home
refresh and actual playback. Check an unfollowed future episode stays hidden
without verified evidence. Discovery depends on the configured indexer's
release and source health; this change cannot manufacture Friday timestamps
that providers do not publish. No production or lab service was touched.
