# Section shelf generations (#285, v0.3.5)

A section recommendation shelf is a bounded 200-candidate build. Apply its
section view, viewer visibility and availability before calculating positions.
Deduplicate the final rendered identities by ratingKey before selecting visible cards.
Retain their ordered title snapshots as the immutable generation. The initial shelf
and its continuation are slices of this generation.

`key` retains the existing PMS route (including collection children), with an
opaque `etherShelf` query parameter. `hubKey` retains the PMS list of displayed
metadata keys and carries the same generation. Dereferencing hubKey returns
that displayed subset, not the entire shelf. Size is the number actually
returned; totalSize on a continuation is the canonical generation length;
More describes remaining items in that generation. Continue Watching and
Recently Added keep their existing live routes and behavior. Home and detail
page projections are unchanged.

A fresh screen build may select a new generation after five minutes or a viewer
revision change. Presentation expiry, hourly/daypart owner builds, availability
evidence and section navigation cannot change an already advertised generation.
Continuation requests resolve the generation directly, without recommender or
collection reconstruction. Watch badges and artwork enrichment render from the
retained titles on each request; live availability cannot recut their membership. Watch-state changes can select a new generation for
a new screen without revoking older continuations. Section screen caches include
client identity because their row URLs are scoped to that client.

Generations are scoped to the authenticated viewer, client, rendering profile,
language, settings and (for restricted viewers) policy epoch. Policy/settings
changes invalidate affected continuations rather than exposing stale policy
answers. Retention is process-local, bounded to 512 generations and 32 MiB of
encoded title snapshots (Go heap usage is larger). Memory eviction, restart, or routing
a continuation to another node can return HTTP 410. Missing generations never
fall back to rebuilding an old offset. Clients must reload the screen to obtain
a current generation after 410. This change does not introduce distributed or
durable snapshot storage.

## Investigation and offline evidence

The reference corpus moved in b90fd5159:
`internal/pmsconform` → `internal/conform/plex` and
`testdata/pms` → `testdata/conform/plex`.

`web_json_hubs_sections_{{section}}.json` records collection shelf keys as
`/library/collections/{key}/children`, curated keys as
`/hubs/items?identifier=...&curatedHubSeed=...`, and hubKey as an explicit
`/library/metadata/{key1},{key2},...` list. Section hubs omit totalSize and report
the actual returned size plus More. `internal/conform/plex/plex.go` records the
Plex Web/Chrome profile and X-Plex-Container-Start/Size paging parameters, with a
50-item listing probe. The corpus does not record a complete horizontal-scroll
network trace or the client's implementation of short-page filling. Thus exact
Plex Web follow-up timing, arbitrary extra query parameters and the repeated
poster fill hypothesis are not established by those recordings.

Confirmed mechanisms on release/0.3.5 (8ddac1937):

- `hubItemsKey` advertises collection children for keyed rows. Those requests
  enter `collectionItems` and `ScopedCollection.Page`, bypassing shelfItems.
  Even a nonexpired shelf cut cannot protect that continuation.
- `collections.Service.Items` applies sectionKeys BEFORE slicing. There is no
  confirmed filter-after-offset defect in that smart-collection implementation.
  However, this independently served collection generation can differ from the
  section's earlier personalized, filtered shelf. `findHub` also preferred a
  matching Home row over section rows for explicitly scoped hub requests.
- `collectionItems` renders availability AFTER collection pagination and then
  reports page.Total from before that rendering. A short response can therefore
  promise more items than it returns. The shelf renderer likewise truncated
  before availability, and formed hubKey from candidates rather than returned
  metadata. Generation-backed section shelves now derive all three from the
  final canonical list.
- Shelf cuts expire after five minutes; personalize/presentation.go keeps its
  presentation only for five minutes and includes context in its key. Later
  pages could therefore see a new candidate order. The fix's versioned URLs
  remove this dependency from continuation requests entirely.
- A producer returning duplicate ratingKeys was passed through unchanged.
  This was reproduced with injected source duplicates; it is a defensive case,
  not evidence that the reporter's source list contains duplicates.

A temporary offline HTTP harness replayed the original and fixed code with the
same four-title section rows and advertised keys. Between the initial count=2
response and Start=2/Size=2 continuation, it advanced six hours and reordered the
collection. On BOTH sections, the original returned 3/4 unique titles with one
repeat; the fix returned 4/4 with zero repeats. Changing availability then made
the original continuation return size=0 with totalSize=4; the fix retained
size=2/totalSize=4. An injected three-copy source returned size=3 before and
size=1 after. The screenshot independently confirms adjacent repeated posters;
server evidence cannot prove the client synthesized those posters.

The lasting `TestShelfGenerationPagingInvariant` covers collection and generic
hub keys, both media types, fixed and changing availability, twelve seeded
partitions per case, arbitrary page sizes, shuffled requests, retries, six-hour
jumps, section switches, changed producer order, source duplicates, hubKey
subsets, cross-client rejection and missing-generation failure. Its union is
exactly the original ordered list, with no duplicate positions or missing keys.
It is an HTTP property test with offline sources; it simulates rebuild results
rather than launching a real Plex Web client or recommender timers.

## Cost

Same opt-in `TestDigitalLargeCatalogueLatency`, same machine and fixture:
300,000 synthetic SQLite movies, 299,400 settled digital dates, 600 unknown;
a 200-item shelf. Twenty requests per operation; serialization included.
These are local measurements, not a production throughput guarantee.

| Operation | Original p50 / p95 | Fixed p50 / p95 |
| --- | --- | --- |
| /hubs/sections/1 | 0.708 / 1.270 ms | 0.699 / 0.989 ms |
| 50-item row continuation | 2.845 / 3.275 ms | 1.344 / 1.827 ms |

Cold section response: 329.5 ms original, 315.5 ms fixed (includes initial SQL
availability cut). Cold row response: 3.436 ms original, 1.580 ms fixed.
The fixture measures a movie shelf on a large digital catalogue; mixed rows
and TV section transitions are covered by the invariant, not this timing run.
Run with `ETHER_285_MEASURE=1 go test ./internal/plex -run TestDigitalLargeCatalogueLatency -count=1 -v`.

## Gate output

Only the touched Go package was checked; no repository-wide build, test or race suite.

```text
$ go test ./internal/plex -count=1
ok  github.com/braedonsaunders/ether/internal/plex  40.451s
$ go vet ./internal/plex
(exit 0, no diagnostics)
$ ETHER_285_MEASURE=1 go test ./internal/plex -run 'TestDigitalLargeCatalogueLatency|TestShelfGenerationPagingInvariant|TestGolden/hubs_section_1$' -count=1 -v
PASS
ok  github.com/braedonsaunders/ether/internal/plex  16.166s
$ git diff --check
(exit 0, no diagnostics)
```

## GitHub reply draft (not posted)

The second #285 fix is ready for v0.3.5.

I confirmed that a collection shelf's first cards and its later pages could
come from different builds: collection-children requests bypassed the shelf
cut. Collection pages could also lose items during availability rendering while
still reporting the earlier total. In an offline reproduction on both Movies
and TV Shows, four titles became only three unique titles with one repeat after
a rebuild. A separate availability change returned zero cards while still
reporting four items.

Each section recommendation shelf now has a type-filtered, deduplicated ordered
generation. Its initial cards, paging key and counts come from that generation;
later pages keep its generation key across idle time and section switches.
The same reproduction now returns all four titles exactly once, with consistent
counts. A lasting invariant test covers both collection and generic hub paging,
idle/daypart changes, section switches, retries and changing availability.

On a 300,000-title offline digital catalogue, median section hubs measured
0.70 ms and 50-item row pages 1.34 ms (previously 0.71 ms and 2.84 ms).
The screenshot's repeated-poster fill behavior remains a client-side hypothesis;
the recorded corpus does not prove how Plex Web fills a short page.

Snapshots are bounded in memory. A restart, eviction or node change can require
a screen reload; an old key fails explicitly instead of silently paging a new
list. Please retest Movies → Recommended and TV Shows → Recommended after idle
and section switching when v0.3.5 is available.
