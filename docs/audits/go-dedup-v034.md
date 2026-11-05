# Go deduplication review — v0.3.4

Branch: `chore/go-dedup`. Fixed handover base: `3c6b315dc` (the local
`release/0.3.4` ref moved during validation, so it is not the diff baseline).
All ten ranked items were reviewed. The retained implementations below are
explicit rejections of a mechanical replacement, with their differing contracts.
No changes to Live TV, `plex/livetv_api.go`, deleted subtitle transcoding code,
FetchFormatJSON, or the bug worker's truncation/Bearer/Retry-After/parsing work.
No owner-console copy was added; locale synchronization does not apply.
Core changes add helpers and tests only.

## Item 1 — flight sharing: fixed / specialized patterns retained

Commit `fba5b1541` (handover, verified): TRaSH and Dictionarry now use
`flight.Group.DoDetached` with bounded work. The same commit fixes a live
DetailPlan waiter inheriting the first caller's canceled/deadline result: it
replans under the waiter's context. `internal/flight` itself is unchanged.

These audit locations exist, but are not interchangeable per-key function calls:

- `internal/subtitles/correct.go:167`: returns immediately with a started/not-started boolean; `wait` separately observes an existing correction for a bounded duration. Do/DoDetached wait for a result and have no atomic try-start/observe API.
- `internal/sessions/detail_plan.go:31`: combines capacity admission, a 30-second cache, authenticated scope, source identity/revision revalidation and close handling. Removing that state would lose more than flight sharing; the cancellation drift is fixed in the handover commit.
- `internal/debrid/provider.go:499`: is a singleton account-list refresh, not a per-key map. `background` must return immediately and `reuse` may wait only on an already-running refresh, without starting one. Keep its bounded detached refresh and observable handle.
- `internal/newznab/client.go:1386`: tracks readers, body cloning/ownership, budget class and cancellation when the last reader leaves. Detached flight would keep spending an indexer grab after all readers leave; leader-owned flight would cancel surviving readers.
- `internal/plex/home_cache.go:111`: is a queued home-cache build barrier tagged with the hard revision; cached/stale responses, latest request templates, queue admission and hard/soft stamps participate in whether a waiter may join.
- `internal/personalize/build.go:23`: is a dirty-rebuild queue: a request during a running build sets `again`, and the worker rebuilds before closing the shared handle. Plain flight drops this invalidation.
- `internal/overlays/cache.go:224`: cancels rendering when the last waiter leaves and removes that precise flight. Detached flight does not implement this ownership contract.
- `internal/playback/playback.go:981`: distinguishes play-request retry grace from immediate detail/surface abandonment, joins compatible alternate keys, manages source retirement, and participates in shutdown. A one-call-per-key group loses these rules.

Validation: resolver TRaSH/Dictionarry/variant/console/health/history tests,
sessions quick tests and flight quick tests. Playback focused preparation/index/
resume tests, overlay quick tests, and debrid listing/duplicate/provider tests
exercise the retained ownership rules.

## Item 2 — resolution and source labels: fixed

Commit `f119a720a`; contract test/import correction `1b8e33c0e`.
`core.ResolutionTier(width,height)` and `ResolutionLabel` use Plex's existing
3200-wide / 1800-high UHD boundary, with the existing 1080/720/576/480/SD
thresholds. Used by Plex conversion and variant classification, playback warm
resolution, overlay presentation, Silo's measured-video badges and Studio replay.
Shared `core.SourceLabel` replaces resolver/Plex source-name switches, including
resolver racing's call site.

Plex's numeric `videoResolution` strings, SD fallback and variant comparison's
coarser sub-720 class are preserved by adapters. Studio keeps its `2160p`
spelling. Silo no longer labels a 1920x800 probed file as raw `800p`.
Overlay quality now recognizes 3836x1600 as 4K, while retaining its historical
`SD` badge below 720 (an existing test caught the need for this adapter).

Validation: core, overlays and Silo quick tests; Plex
`Resolution|Variant|Quality|NativeProbeFacts|Slug|Paging`; Studio replay/ranking;
resolver variants and playback warm-resolution tests.

## Item 3 — atomic writes: fixed

Commit `40bc1c0cf`; persistence contract test `47902c060`.
`internal/atomicfile.Write` uses a unique same-directory temporary file,
permissions, file fsync, close, rename, then directory fsync. Config delegates
through its existing injectable directory-sync hook. A post-rename sync error
warns and returns success because the new file is already live; Windows retains
the no-directory-fsync and permission behavior.

Replaced manifest writing in cachefill, playback resume NZBs, resolver custom
formats, Newznab NZB caching, subtitle sidecars, backup reports, and single
Plex certificate/key writes. Backup and Plex directory sync delegate to the
shared helper. Backup no longer collides on a fixed `path + ".tmp"`.
Plex's two-file certificate/key replacement retains `writeTemp`: both files
must be staged and synced before either rename, which a single-file Write
cannot express. Its final directory sync is shared.

Validation: atomicfile concurrent whole-file replacement; config atomic/dir-sync
and quick tests; cachefill, playback resume, resolver, Newznab, subtitles, backup
and plextv quick/focused tests.

## Item 4 — store helpers: fixed

Commits `12748a42c`, `0d272b065`.
Added `store.Placeholders`, `Chunks`, `ForUpdate` (and shared SQL `BoolInt`).
Empty placeholder input returns `NULL`, never an empty SQL fragment; existing
list guards remain at call sites. The three named placeholder functions delegate
to it, along with mechanical IN-list construction in availability, collections,
resolver health, library filtering and playback/Plex cluster state.
Catalogue hydration and related 200-row loops use shared chunks. Plexwatch row
locks use the shared suffix. Alias-specific `FOR UPDATE OF g,u` clauses are
retained because they lock selected aliases, not every joined row. SQL trigger
bodies and fixture SQL builders retain their separate contracts.

Validation: store, catalogue, artwork service, availability, libraryfilter,
collections, personalization, resolver, playback, Plex and plexwatch tests.

## Item 5 — JSON response writers: fixed

Commits `1c2d11404`, `dde83586a`.
`httpjson.Write` encodes to a buffer before committing headers, sends 500 on
encoding failure, and preserves Encoder's newline/HTML escaping on success.
The seven named packages delegate to it (Extend's error envelope is preserved),
as does Studio replay's identical writer. Writer wrappers keep private APIs
stable. Protocol-specific encoders, raw JSON forwarding, streaming output and
other content-type contracts remain separate; Live TV is excluded by ownership.

Validation: status-before-body/escaping contract test, plugins, Studio replay,
catalogue, remote, MCP, agent and Extend tests.

## Item 6 — pagination: fixed

Commits `f0cd3cba4`, `44ef846b1`.
Paging now owns strict limit/offset parsing and saturating page multiplication.
Remote page offsets cannot wrap; resolver negative/overflowing offsets are
rejected to the existing default. Silo shares decimal limits/cursors across
catalogue, similar, not-interested, people, episode and home-item limits.
Empty people `?limit=` uses 20. Encoded episode cursors retain their format,
with headroom reserved for safe next-page arithmetic.

Validation: paging overflow/default contract test, Silo quick tests, remote and
resolver console tests.

## Item 7 — small stdlib copies: fixed / differing contracts retained

Commit `407a33a4e`; TRaSH slug caller in `635251bb8`.
Identical nonempty selection uses `cmp.Or`, membership uses `slices.Contains`,
and the three in-scope sorted-key helpers use `slices.Sorted(maps.Keys(...))`.
Sorted-key wrappers deliberately retain non-nil empty slices. SQL boolean
conversions share `store.BoolInt`; the two in-scope integer absolute-value
implementations share `numutil.Abs` with existing MinInt behavior. Go has no
stdlib integer Abs function. Agent IDs use `rand.Text()` after their type prefix.
Agent, LLM and TRaSH ASCII slugs share `textutil.Slug` with individual limits and
fallbacks; truncation no longer leaves the agent slug ending in a dash.

Explicit rejections / audit count drift:

- Collections, plugin catalogue and plugin adapters have whitespace-aware
  firstNonEmpty variants, not cmp.Or equivalents; some return the original
  untrimmed value and adapters return a trimmed value. Keep that distinction.
- The fourth sortedKeys and third containsString cited by the audit live in
  Live TV and are excluded. Only two absInt implementations are in scope.
- `hoststat/resources.go:243` returns its argument unchanged inside bounds,
  including negative zero; `sysload/sysload.go:258` uses min/max and produces
  positive zero. Unifying them changes JSON-visible floating point spelling.
- The remaining IDs in config/defaults.go:12, playback/session.go:493,
  spike/playback.go:53, cluster/identity.go:80, diagnostics/recorder.go:823 and
  plexwatch/fence.go:215 deliberately retain fixed byte-count hexadecimal
  formats (and their error signatures), rather than silently changing existing
  persisted/protocol identifier shapes. Live TV randomID is excluded.
- Plex slugify in `plex/slug.go:14` folds Latin accents, expands ampersands and
  removes apostrophes, and is client-visible. Personalize's genreSlug expands
  ampersands but otherwise only substitutes spaces. Neither is the ASCII
  punctuation-collapsing slug contract; replacing them changes IDs/row names.

Validation: all changed packages compile; their quick/focused tests pass,
including agent/builtin, config, auth, state, stats, Plex, TMDB, notify, stremio,
watchparty and prerolls. Wrappers required by existing test fixtures stay tiny.

## Item 8 — container parsing: fixed primitive / specialized walkers retained

Commits `307c6b8e8`, `a579126f4`.
Shared `internal/container/ebml` owns width, integer and unknown-size encoding.
Native probing, transcode chunk parsing (and therefore mkv_hls), playback index,
subtitle extraction and stream damage delegate to it. Strict ID readers retain
the four-byte limit; chunk block/lacing reads still allow eight-byte integers.

Element traversal is deliberately retained: nativeprobe's slice nodes reject
truncated bodies; playback/subtitle range readers bound unknown sizes to their
parent and map failures to distinct sentinels; mkv_hls's ReaderAt header reader
allows sizes beyond the available tail and exposes unknown length as -1;
stream damage performs priority-aware incremental reads while preserving CRCs.
These are differing contracts rather than interchangeable walkers. MP4 walkers
similarly mix in-memory probe trees, range-index reads and streaming copying/
filtering; no single mechanical replacement was established without changing
truncation/error/ownership behavior, so that optional consolidation is rejected.

Validation: all eight VINT widths, unknown sizes and short buffers; transcode
Matroska/TS/fMP4 chunks and assembly; playback index; subtitle container tests;
stream damage/CRC tests. Existing behavior is preserved.

## Item 9 — LLM JSON and titles: fixed

Commit `c224543ed`.
Exported `llm.ExtractJSON` preserves bracket matching, string escaping, reasoning
stripping and JSON repair. Collections wraps it with its object-only/valid-JSON
contract. Langfeat uses it instead of first-open/last-close slicing, so later
prose or another JSON fragment does not get swallowed into one invalid value.
LLM title matching and migration previews use releaseparse.NormalizeTitle for
accent folding and the shared title-comparison rules. The bug worker's
truncation helpers/sites are untouched.

Validation: llm quick tests, collection extraction/generation, langfeat and
migration quick tests.

## Item 10 — caches, redaction, migration and identity: fixed / specialized state retained

Commit `635251bb8`.
Newznab descriptors and TRaSH's byte/entry-bounded files use `internal/lru`.
TRaSH reads use Peek to preserve write-age eviction rather than changing cache
recency on hits. Descriptor reads still enforce expiry. Shared
`logredact.TransportError` handles wrapped URL errors and preserves the cause;
callers retain their distinct masking policies: TMDB masks api_key, plex.tv
removes userinfo/query/fragment, and Newznab omits the entire URL. It does not
reuse database-only store.RedactURL, which recognizes different credentials.
Identical `Ether` outbound User-Agent defaults share `httpclient.UserAgent`.
Plex identification, browser emulation, required provider app identities and
versioned/special-purpose agent values remain explicit to preserve behavior.

Remaining scan-to-evict sites are explicitly retained:

- `llm/service.go:307` skips cache keys with active flights; a generic capacity
  eviction may remove state owned by an executing request.
- `personalize/engine.go:834`, `presentation.go:78`, `ranker.go:427` are
  time/generation-bounded snapshots that participate in stable home-row and
  ranking replay. Access-based LRU would change which snapshots survive and
  therefore home responses; preserving those response contracts takes priority.
- `debrid/media_pause.go:68`, playback warm misses, resolver check histories,
  pending stream repair slots and source cursor limits are backoff/history/
  coordination state, not ordinary reusable-value caches. Their deadlines or
  generation ownership determine removal.

MigrateSecrets is not a family of identical loops: trakt/secrets.go:47 migrates
paired account tokens with compare-and-swap predicates; notify/secrets.go:261
migrates per-channel JSON and destination-bound secrets; ratings/ratings.go:209
reads one settings row, not a loop; cardigann/service.go:346 resolves indexer
origins before delegating session migration. Their encryption/AAD, error and
persistence contracts differ. Live TV is excluded. No common mechanical loop
was established, so this conditional refactor is rejected rather than touching
another worker's security/deletion scope.

Validation: Newznab and resolver TRaSH tests, TMDB/plextv/logredact tests,
provider listing tests, and quick tests for changed outbound clients.

## Validation and size

Final gate: `GOMAXPROCS=2 CGO_ENABLED=0 go build -p 1 ./...` and
`GOMAXPROCS=2 CGO_ENABLED=0 go vet -p 1 ./...` both exited 0 (no output).
`git diff --check` passed. Tests ran serially, one package at a time; the large
packages used the focused selectors listed above. No full Go test suite or
browser/dev-server processes were started.

An initial `debrid -short` run reached the 60-second cap in
`TestCDNOutageRefreshIsBoundedAndTransient`; this is not claimed as a passing
full-package run. The final focused listing/duplicate/provider run passes.
The first package inventory used the moving release ref and attempted the
absent `internal/conform/openapi/spectest` directory; this was a harness path
selection error, not a Go source failure. The fixed handover base is used for
all change/size accounting. Unrelated packages picked up from that moving ref
are omitted below.

Final supplementary test output (the following build and vet commands also
completed successfully):

```text
ok  	github.com/braedonsaunders/ether/internal/debrid	0.289s
ok  	github.com/braedonsaunders/ether/internal/atomicfile	0.334s
ok  	github.com/braedonsaunders/ether/internal/agent	0.381s
ok  	github.com/braedonsaunders/ether/internal/frontends/silo	1.493s
ok  	github.com/braedonsaunders/ether/internal/logredact	0.200s
ok  	github.com/braedonsaunders/ether/internal/conform	0.236s
ok  	github.com/braedonsaunders/ether/internal/flight	0.249s
```

Other focused/quick package outputs from the serial validation run:

```text
ok  	github.com/braedonsaunders/ether/internal/admin	11.290s
ok  	github.com/braedonsaunders/ether/internal/agent	0.387s
ok  	github.com/braedonsaunders/ether/internal/agent/builtin	0.320s
ok  	github.com/braedonsaunders/ether/internal/animemap	0.289s
ok  	github.com/braedonsaunders/ether/internal/artwork/service	0.795s
?   	github.com/braedonsaunders/ether/internal/atomicfile	[no test files]
ok  	github.com/braedonsaunders/ether/internal/auth	0.347s
ok  	github.com/braedonsaunders/ether/internal/availability	4.139s
ok  	github.com/braedonsaunders/ether/internal/backup	20.884s
ok  	github.com/braedonsaunders/ether/internal/cachefill	0.901s
ok  	github.com/braedonsaunders/ether/internal/cardigann	0.654s
ok  	github.com/braedonsaunders/ether/internal/catalog	9.421s
ok  	github.com/braedonsaunders/ether/internal/collections	0.529s
ok  	github.com/braedonsaunders/ether/internal/config	3.019s
ok  	github.com/braedonsaunders/ether/internal/conform	0.358s [no tests to run]
ok  	github.com/braedonsaunders/ether/internal/container/ebml	0.277s
ok  	github.com/braedonsaunders/ether/internal/core	0.354s
ok  	github.com/braedonsaunders/ether/internal/experience	0.401s
ok  	github.com/braedonsaunders/ether/internal/extend	0.715s
ok  	github.com/braedonsaunders/ether/internal/frontend/kit/paging	0.265s
ok  	github.com/braedonsaunders/ether/internal/frontends/silo	1.708s
?   	github.com/braedonsaunders/ether/internal/httpclient	[no test files]
ok  	github.com/braedonsaunders/ether/internal/httpjson	0.214s
ok  	github.com/braedonsaunders/ether/internal/langfeat	0.359s
ok  	github.com/braedonsaunders/ether/internal/libraryfilter	0.318s
ok  	github.com/braedonsaunders/ether/internal/llm	0.319s
ok  	github.com/braedonsaunders/ether/internal/mcp	0.563s
ok  	github.com/braedonsaunders/ether/internal/migrate	6.874s
ok  	github.com/braedonsaunders/ether/internal/newznab	1.631s
ok  	github.com/braedonsaunders/ether/internal/notify	0.713s
?   	github.com/braedonsaunders/ether/internal/numutil	[no test files]
ok  	github.com/braedonsaunders/ether/internal/overlays	0.453s
ok  	github.com/braedonsaunders/ether/internal/personalize	1.679s
ok  	github.com/braedonsaunders/ether/internal/playback	6.652s
ok  	github.com/braedonsaunders/ether/internal/plex	0.586s
ok  	github.com/braedonsaunders/ether/internal/plextv	9.185s
ok  	github.com/braedonsaunders/ether/internal/plexwatch	0.626s
ok  	github.com/braedonsaunders/ether/internal/plugins	1.801s
ok  	github.com/braedonsaunders/ether/internal/plugins/catalogue	0.348s
ok  	github.com/braedonsaunders/ether/internal/plugins/remote	0.659s
ok  	github.com/braedonsaunders/ether/internal/plugins/studio	0.824s
ok  	github.com/braedonsaunders/ether/internal/prerolls	0.287s
ok  	github.com/braedonsaunders/ether/internal/remote	2.846s
ok  	github.com/braedonsaunders/ether/internal/resolver	4.992s
ok  	github.com/braedonsaunders/ether/internal/sessions	4.820s
ok  	github.com/braedonsaunders/ether/internal/state	0.477s
ok  	github.com/braedonsaunders/ether/internal/stats	0.524s
ok  	github.com/braedonsaunders/ether/internal/store	1.971s
ok  	github.com/braedonsaunders/ether/internal/stream	0.455s
ok  	github.com/braedonsaunders/ether/internal/stremio	0.332s
ok  	github.com/braedonsaunders/ether/internal/subtitles	4.004s
?   	github.com/braedonsaunders/ether/internal/textutil	[no test files]
ok  	github.com/braedonsaunders/ether/internal/tmdb	3.725s
ok  	github.com/braedonsaunders/ether/internal/trakt	2.023s
ok  	github.com/braedonsaunders/ether/internal/transcode	0.279s
ok  	github.com/braedonsaunders/ether/internal/watchparty	0.216s
```

Production Go delta from `3c6b315dc`: **697 added, 979 removed = 282 lines
saved**. Including contract tests: **817 added, 981 removed = 164 lines saved**.
Documentation is excluded from these code counts. All implementation and
contract-test commits are on `chore/go-dedup`; the report is committed separately.
