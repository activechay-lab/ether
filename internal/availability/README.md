# Availability monitoring

Ether observes recent releases and reuses successful resolver health checks. It
never removes catalogue/search results because evidence is missing. Movie dates
and episode air dates still use the catalogue's existing release eligibility;
quality, CAM acceptance, identities, blocklists and episode targeting use the
resolver's existing policies rather than a second rules engine.

## Owner controls

Sources → Availability provides monitoring, selective validation, participating
feeds, debrid participation, per-type release modes (`MovieMode`, `ShowMode`,
falling back to `Mode`), preference for checked recommendations and optional
newly available shelves. Monitoring and validation start disabled. Presentation
preferences start enabled and can benefit from ordinary playback observations.

Default daily background allowances (UTC, shared across nodes):

- Per indexer: 10% (Gentle 5%; Unlimited up to 100 − `reservePercent`) of its
  daily API limit: the owner's, else the one it reports (Newznab `apilimits`),
  else 1,000 (the strictest private tier), so 100 feed and search requests by
  default. The day's remaining head polls are reserved, so gap pages and title
  searches cannot starve the feeds; a feed whose allowance cannot fit 30-minute
  polls in half of it is polled less often. Requests to one indexer are at least
  `Deps.Pace` (3 s) apart.
- In total, the sum of those (or the owner's custom total).
- 100 validation API requests, 20 candidate checks and 20 descriptor grabs.
- 512 MiB of conservative data reservations. NNTP BODY reservations use the
  provider's maximum article size, so actual transferred bytes can be much lower.

An allowance of zero disables that work. Foreground requests still have their
normal account limits. Every feed page, provider API request and source read goes
through admission before network work. Ordinary playback does not use the
background allowance. Shared worker contexts retain the admission hook.

## Sticky release gate and bounded retry schedule

Newznab/Torznab recent-release pages use an empty `t=search` request, following the
incremental model documented by [Radarr](https://wiki.servarr.com/radarr/faq#how-does-radarr-work).
Built-in sites list their latest page (a keywordless search, as Prowlarr does);
Stremio addons and plugin sources are search-only. One page describes up to 100
releases. Head polls default to every 30 minutes. A sync reads from the head
back to the previous head: one page usually, otherwise only the pages the gap
needs (at most `MaxPages` per sync), resuming an unfinished gap every minute
while its allowance has room. A first sync starts at the head; **catch-up**
(optional) also walks recent history. Failed requests do not advance the cursor.

In-window titles without an open gate row are checked via an indexed due-retry
table (`availability_title_checks_due`) anchored to the movie's digital/physical
release date (falling back to theatrical) or the episode's air date on the
bounded ladder `[0, 30m, 1h, 90m, 2h, 150m, 3h, 4h, 8h]` and then every `24h`
until the evidence window ends. At most `probeBatch` (10) due titles are searched
per minute, ordered by priority (unchecked first-look, watchlisted, and post-date
titles first), asking one source at a time and stopping at the first acceptable
release. On an idle server with no due rows, the scheduler performs one indexed
due-retry query returning zero rows and no catalogue scan or set rebuild.

Once a title's gate (`availability_gate`, keyed by `(policy, rating_key)`) opens
under a policy fingerprint, it **never closes**: `Invalidate` retracts that
specific release's evidence in `availability_releases` and console evidence so
playback/search move on to alternative releases, and time passing never re-hides
the title. Only a policy, profile, or source-generation change starts a fresh
gate policy fingerprint.

Because opening is irreversible, opening is strict against pre-release fakes:

- **Verified evidence** opens the gate immediately: a successful background
  health check that read real video (`VerifyFeedCandidate`), a definitive cached
  debrid copy (`CacheCheck`), or successful ordinary playback (`Observe`).
- **Unverified feed/search matches** open the gate immediately **only after** the
  title's official date has passed (movie digital/physical release date, episode
  air date). Before that date, an unverified profile-accepted match leaves the
  gate shut and queues the candidate in `availability_releases` for background
  verification within the validation allowance.

Matching scans local title identities and resolves episodes locally; it does not
call TMDB for every feed item. Conflicting identifiers and ambiguous title matches
are rejected. A season pack only contributes known, already-aired episodes, and
its health check is targeted to the chosen episode using the existing resolver.

Library browse, rows, facets, episode counts and search evaluate the sticky gate
directly inside their existing SQL queries via an indexed `EXISTS` lookup on
`availability_gate(policy, rating_key)` combined with request-clock window
bounds — zero extra round trips, zero Go-side filtering of title lists, and zero
in-memory hidden-set recomputation. Durable release verdicts live in
`availability_verdicts` (pruned with feed items and capped at 60,000 rows) so
policy changes reuse or re-evaluate retained observations cleanly.

Recommendation priority and newly available shelves still require fresh (6-hour)
checked evidence (`verifiedKeys`), distinct from sticky browse visibility.

## Operations

The existing cluster leader runs one scheduler; cursors, sticky gate rows,
observations and atomic usage counters are durable in the shared database. Hot
settings are respected by admission. Checks pause while local playback is active.
Each cycle is bounded, cancellation propagates, transient outcomes back off, and
descriptors expire after 30 days with a hard retention cap of 30,000 observations.
Signed release locators are sealed using the config store; owner status never
exposes them.
