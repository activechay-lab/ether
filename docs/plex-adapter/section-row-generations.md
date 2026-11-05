# Section recommendation shelf generations

Section shelves assemble a bounded candidate window. Their section type,
viewer policy and availability filters run before positions are assigned.
The final ordered rating keys are deduplicated before any page is selected.
The initial shelf and all continuations select positions from that generation.

The existing PMS `key` and `hubKey` URLs carry an opaque `etherShelf` parameter.
`hubKey` still explicitly names only the displayed metadata subset. The
original route containers and metadata/detail renderers remain in use. Counts
refer to the canonical list, while watched badges and artwork enrichment render
from current presentation data. Continue Watching and Recently Added retain
their live behavior; Home and detail-page shelves retain their existing URLs.

`plex_shelf_generations` in the application's SQLite/PostgreSQL store retains
ordered rating keys, a scope hash, the continuation path and an indexed expiry.
It never stores titles or rendered metadata. A screen build collects its new
or near-expiry generations into one batch and offers it without SQL or a wait
for capacity. One server-owned background writer coalesces identities for
20 ms and persists the needed rows in one transaction. The queue holds at most
256 pending generations plus one equally bounded active batch; overload drops
an offered batch. Each persistence attempt has a one-second context budget,
and server shutdown cancels it rather than flushing on the request path.

The writer reads shared retention first. Existing generations with at least one
day remaining acquire no write transaction, including after local eviction or
restart. Expiry refresh is eligible only below one day remaining and at most
once per generation per hour. Refresh updates only expiry, and new generations
use insert-on-conflict-do-nothing. Expired-row pruning shares a necessary batch
transaction, at most hourly; it never creates a warm-repeat write by itself.

All HA nodes use the shared store. Once a batch commits, another node or a
restarted server reconstructs the page from catalogue titles in retained order.
Until then, the producing node uses its in-memory cut. Normal shared durability
lags enqueue by the 20 ms coalescing window plus database work; one queued batch
can also wait behind an active one-second attempt. This is an attempt budget,
not a durability guarantee: overload, timeout, shutdown or write failure can drop
the batch. A later screen build can retry an unpersisted local generation.
Failover during that gap uses the graceful current-page fallback below, so it
can lose continuity but never breaks a shelf or requires reload.

A screen build can select a new generation after five minutes or a viewer
revision change. Advertised generations remain usable across presentation
expiry, daypart rollover, evidence updates, section navigation and node changes.
Shared rows are retained for seven days from publication/refresh; expired rows
are pruned opportunistically, at most hourly per serving process. The bounded
local cache holds at most 512 generations and approximately 32 MiB of encoded
title data, excluding Go heap overhead.

Scope uses stable user data, the shared policy epoch for restricted viewers,
and the client/rendering profile. Node-local configuration/layout counters and
user timestamp formatting are not part of cluster identity. The token never
grants authorization; current viewer policy still applies.

If a generation has expired or cannot be recovered, the existing route serves
a current build. Collection recovery deduplicates the filtered current list
before slicing it; generic hub paging uses its canonical current row. This
fallback can lose continuity with earlier pages, but it never reports a missing
generation as HTTP 410 or requires a client reload. Authorization failures
continue to fail closed. Requests without the parameter use an allocation-free
query lookup and otherwise follow their original handlers.
