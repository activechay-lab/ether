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
It never stores titles or rendered metadata. A new generation is persisted
before its URL is returned. All HA nodes use the shared store; another node or
a restarted server reconstructs the requested page from cached catalogue titles
in the retained order. The local memory cache is only an acceleration.

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
