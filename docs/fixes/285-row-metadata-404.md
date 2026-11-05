# #285: list cards that cannot open

Branch `fix/285-row-metadata-404`, based on `release/0.3.5` at `95d6c007a`.
Original fix `472e93e92`; this follow-up replaces its admission loader after cost review.

## Cause and evidence

Read the newest arjanterheegde comment with `gh issue view 285 --comments` and inspected the supplied collection-editor screenshot as untrusted image data.

The offline baseline reproduced two mismatches: a row rendered three cards where only the released and digitally released movies should survive; catalogue lookup rejected a movie with a past NL digital date and future theatrical date, in both Immediate and Digital modes.

List summaries previously bypassed the catalogue's release/digital admission gate. Separately, metadata, artwork, availability and playback checked the theatrical date independently of passed home evidence. A missing mirror record alone was not an unconditional 404: catalogue Get already fetched missing metadata on demand.

The reporter's database and TMDB response were not supplied. These are confirmed mechanisms, not proof of the particular stored value that rejected EB. Its reported May/October 2026 dates have not been independently checked against a live provider.

## Current implementation

- `Catalog.TitleOpenable` is a predicate over loaded title facts and current catalogue settings/time. It performs no database access, mirroring or network work. Both Plex row admission and detail visibility call it.
- `visibleTitle` no longer enters a bulk loader. It retains the prior viewer/library, inherited-show, availability and Plex rating checks after the pure catalogue predicate.
- `LoadRowFacts` belongs at row assembly. It reuses canonical cached titles; cold batches load release, year, adult/rating and home-release facts together in one indexed query per 200 keys. It never writes. Collection mirror reads also load home facts with their existing title query.
- Delayed source batches cannot overwrite details that finished in the meantime: the UPSERT guards detailed rows atomically, while preserving search promotion.
- Source list refreshes mirror asynchronously. Collection build workers use one sorted transaction for their fresh source batch instead of one transaction per title.
- Missing list titles enter a bounded in-memory summary registry (4,096 titles, one-hour expiry) and are offered to the existing bounded background hydration scheduler. Requests can render/open their summaries without waiting for SQLite's writer. Get and StoredTitle prefer persisted facts, falling back to the source summary on a miss. Full movie/show details hydrate behind a summary open.
- Pinned availability generations retain their cut; current authorization still applies. Counts are fixed after row selection.
- No core fields/interfaces changed in this follow-up. The previous additive `Title.IsFutureRelease` and the passed-home-evidence correction remain.

The first fix put catalogue reads and possible writes inside `visibleTitle`; that design is superseded. No request uses the old `Catalog.OpenableTitles` loader.

## NL release policy

`movieDetailsToTitle` selects the earliest theatrical date across countries (TMDB types 2/3) as `Released`, the date-added source. Title year comes separately from the primary release date. Both displays are preserved.

`saveHomeReleases` stores country/type dates and the earliest global type 4/5 date as home-release evidence. This admission is global; NL selects certification, not a separate Netflix entitlement or viewer-territory policy. A passed NL type-4 release is valid evidence and now beats a later theatrical date for release admission. No provider-name inference or live Netflix lookup was introduced. Normal source/playback restrictions remain enforced.

## Request measurements

Reproducible harness: `ETHER_285_MEASURE=1 GOMAXPROCS=2 go test -p 2 ./internal/plex -run '^TestAdmission(Request|Unmirrored)Cost$' -count=1 -v`.

Same existing realistic fixture before and after: 300,000 titles in a real WAL SQLite database; ten 200-title shelves. Real catalogue/SQL, fake auth, downstream playback and image transport; no live services. Before is `089810a8b` (implementation `472e93e92`). Each endpoint has three samples. Fixture priming happens outside request statistics. QueryStats records all foreground statements; background mirroring is separately tracked by the existing scheduler.

| Request | Statements before → after | Legacy loader calls before → after | Catalogue Get calls before → after | Foreground writes before → after |
| --- | --- | --- | --- | --- |
| Metadata, warm | 10 → 0 | 2 → 0 | 1 → 1 | 0 → 0 |
| Thumb, warm, 200 response | 0 → 0 | 0 → 0 | 1 → 1 | 0 → 0 |
| Photo transcode, warm, 200 response | 0 → 0 | 0 → 0 | 1 → 1 | 0 → 0 |
| Playback start, warm | 0 → 0 | 0 → 0 | 1 → 1 | 0 → 0 |
| Playback start, first capability issuance | 7 → 7 | 0 → 0 | 2 → 2 | 2 → 2 |
| Home `/hubs` | 50 → 0 | 11 → 0 | 0 → 0 | 0 → 0 |
| Section hubs, first screen | 57 → 2 | 12 → 0 | 0 → 0 | 0 → 0 |
| Section hubs, warm | 5 → 0 | 2 → 0 | 0 → 0 | 0 → 0 |

The retained two cold-start writes are existing `capability_headers` and `capability_claims` inserts: playback start creates a capability. They are not catalogue mirroring, are not newly added, and are excluded from any claim that read endpoints write nothing. This change adds zero admission statements to play start.

The new pure predicate runs twice in this metadata projection and once per emitted candidate in Home (2,000 calls). These calls issue zero SQL. Typical artwork on this fixture never entered the old loader either; the measurements do not claim an artwork SQL reduction that did not occur.

On the same 300k fixture **with SQLite's writer held**, an unmirrored source title returned 200 from `/hubs`, metadata, thumb and photo transcode. Each request made exactly one indexed miss, zero writes, and read zero catalogue rows; the summary supplied the response. Background mirroring completed after releasing the writer. The ordinary offline catalogue invariant covers both movie and show fallback, plus a zero-statement assertion around the predicate.

All raw samples and timings are in `285-admission-costs.json`. Three timing samples are observational, not a latency benchmark: Latency samples are retained verbatim; no general latency improvement is claimed from three observations. The statement counts are the stable acceptance evidence.

## Recorded PMS hubKey evidence

This checkout stores the captures under `testdata/conform/plex`, not `testdata/pms`. Decoded the base64 response bodies and compared each emitted `hubKey` with `/library/metadata/` plus the rendered `Metadata[].ratingKey` in order:

| Recorded capture | Matching hubKeys | Hubs omitting hubKey | Mismatches |
| --- | --- | --- | --- |
| `web/web_json_hubs.json` | 17 | 8 | 0 |
| `ios/ios_json_hubs.json` | 17 | 8 | 0 |
| `appletv/appletv_json_hubs.json` | 17 | 8 | 0 |
| `web/web_json_library_metadata_{{movieKey}}_includeExtras-1&includeRelated~e7bd9d5f62.json` | 4 | 0 | 0 |
| `ios/ios_json_library_metadata_{{movieKey}}_includeExtras-1&includeRelated~7a50f5b6e3.json` | 4 | 0 | 0 |

All 59 emitted hubKeys match rendered cards. This supports retaining the rendered-card projection for Home and movie-detail hubs.


## Focused gates

No full suite, race run, build, browser, live server or production changes.

- Catalogue: row/detail admission in both release modes; conflicting summary/canonical dates; writer-held movie/show read-through and zero-write/zero-predicate-SQL assertions; on-demand detail hydration; primary-year/theatrical-date preservation; asynchronous list order/details/thresholds; adult mirror reads.
- Collections: authoritative upcoming refresh and mixed-refresh failure behavior.
- Plex: row/detail/poster admission; immutable shelf paging across type filtering, restart and nodes; wire parity; asynchronous shelf persistence; restricted viewers, inherited show policy, availability and open-session retention.
- Existing passed artwork, playback and availability gates from the original fix remain applicable; their implementation was unchanged by this follow-up.
- `git diff --check`.

## GitHub reply draft (unposted)

Thanks for the EB example and metadata trace. I reproduced a list/detail admission mismatch and a separate release-date problem: a future theatrical date could hide a movie whose NL digital release had already passed.

Rows and detail now share a cheap admission predicate over the same release facts. Hidden titles are omitted before the section shelf's counts are fixed. Missing list titles can render and open from their source summary while a background batch mirrors them; requests do not wait for or write to the catalogue. Verified home-release evidence prevents a later theatrical date from making a movie appear unreleased, while date added and title year stay unchanged.

On the 300,000-title offline fixture, warm metadata went from 10 SQL statements to 0 and Home hubs from 50 to 0. The writer-held check returned 200 for an unmirrored title's row, detail and artwork with zero foreground writes. Restricted-viewer and shelf-paging checks pass.

The screenshots do not expose EB's stored catalogue dates, so I cannot identify the exact stored value that rejected it on your installation. Please retest its poster and detail page after this fix is integrated into v0.3.5. Normal source/playback checks still apply.
