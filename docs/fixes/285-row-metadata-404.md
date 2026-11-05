# #285: list cards that cannot open

Branch: `fix/285-row-metadata-404`, based on `origin/release/0.3.5` at `95d6c007a`.

## Evidence and cause

Read the last arjanterheegde comment with `gh issue view 285 --comments` and inspected the supplied collection-editor screenshot as untrusted image data. The report's fast metadata 404s and missing Plex poster are consistent with catalogue admission, rather than a playback resolver failure.

Confirmed in code and offline reproduction:

- `RowSelection.Items` starts from list summaries and checks title policy, but does not ask `Catalog.Get`'s release/digital gate. `renderTitles` formerly added availability filtering without the catalogue detail-admission gate. Thus a list summary can survive while the mirrored title is deliberately hidden.
- `Catalog.Get` fetches missing titles on demand already. A missing mirror row alone is not an unconditional 404 in this implementation. Rows nevertheless need to mirror their summaries so stored artwork and subsequent lookups have an identity.
- `movieDetailsToTitle` selects the earliest theatrical release (TMDB types 2/3, across countries) as `Released`. This is also the date-added source. `applyMovieYear` separately selects the primary title year.
- `saveHomeReleases` stores country/type dates and the earliest global type 4/5 date as `HomeReleased`. Region NL affects certification selection; this home-release admission is global, not Netflix subscription or per-viewer territory verification. A passed NL type-4 date therefore is valid home evidence. No provider-name inference or live Netflix lookup was added.
- `Title.IsReleased`, metadata, artwork selection, availability and playback had independent future-theatrical checks. Even with verified past home evidence, a future theatrical date rejected the movie.

The offline baseline reproduced both defects: the row rendered **3** cards where only the released and digitally released movies should survive; direct catalogue lookup of a movie with a past NL digital date and future theatrical date returned **not found**, in both Immediate and Digital modes. Baseline was evaluated with release branch implementations (plus only the additive method declaration needed to compile the new test helper).

**Limit:** the reporter's catalogue database and TMDB response were not supplied. This confirms the mechanisms, not the exact stored date or gate that rejected EB on that installation. The reported May/October 2026 dates were not independently verified against a live provider.

## Fix

- Rows and ordinary metadata now use `openableTitles`: canonical catalogue release/digital admission, current content/library and viewer policy, availability, inherited show policy and Plex rating guard.
- `Catalog.OpenableTitles` mirrors missing movie/show summaries in a batch, reloads canonical facts, and uses the detail-admission gate. Existing mirror facts beat stale list facts. Full metadata still hydrates on open. Virtual episode-order identities are not mirrored.
- Row filtering precedes section-generation counts; `hubKey` names only rendered cards. Existing pinned availability cuts remain pinned, with current authorization enforced.
- A verified passed home-release date satisfies movie release admission even if theatrical release is later. Detail, artwork, availability and playback use the common release decision. Adult, library, rating and source-evidence restrictions remain enforced.
- The theatrical `Released`/date-added value and primary title year remain unchanged. No core fields or interfaces were removed; `Title.IsFutureRelease` is additive.

## PMS reference

Consulted the owner's Rex reference for concepts and recorded facts only; copied no code or fixtures:

- `~/Documents/.ether-ref/rex/docs/protocol-traps.md`, light collection/list versus detail projections (around lines 650–663), and missing-artwork 404 behavior (367–369).
- `rust/src/runtime.rs`, `route_metadata` (18343 onwards): known items retain full detail projection; unavailable identities retain ordinary errors.
- `tests/fixtures/decisions/rows.json` provenance and `tests/fixtures/transcode/session_gate_s1422.json`: actual recorded PMS 1.42.2 decisions/session errors. These contain no TMDB future-theatrical versus NL streaming admission case. They do not establish a regional release policy.

Wire behavior remains ordinary PMS light rows, detail metadata and 404s for hidden identities; no invented error payload or synthetic playable placeholder was introduced.

## Gates

Only focused offline package tests; no full suite, race run, build, live server, browser or production changes.

- Catalogue: list/detail admission invariant in Immediate and Digital modes; conflicting summary versus mirror dates; missing-summary mirroring and on-demand details; theatrical-date and primary-year preservation.
- Plex: row/detail/poster admission invariant; immutable shelf paging across type filtering, node/restart and wire projection; restricted-viewer and inherited-show policy; recent availability and active-session retention.
- Availability: digital paging cut, bounded digital evidence window and published-answer refresh.
- Artwork/playback: stored-only parent lookup, cold selection/revocation, list visibility and future-title refusal before resolver access.
- `git diff --check`.

## GitHub reply draft

Thanks for the EB example and the metadata trace. I reproduced a mismatch between list rows and direct metadata: a list summary could survive rendering while the catalogue's release/digital gate rejected opening it. I also reproduced a movie being rejected for a future theatrical date despite an already-passed NL digital release date.

The fix makes rows use the same catalogue admission and viewer-policy checks as direct metadata. Hidden titles are omitted before the section shelf's counts are fixed, and hub keys name only surviving cards. Missing list summaries are mirrored, with full details fetched on open. Verified digital-release evidence now prevents a later theatrical date from making the movie appear unreleased, without changing its displayed date-added value or title year.

The focused offline checks pass for row/detail/poster agreement, missing summaries, both release modes, restricted viewers and existing shelf paging. The screenshots do not expose EB's stored catalogue dates, so I cannot say which exact stored value rejected it on your server. Please retest EB's poster and detail page after the v0.3.5 fix is integrated. A successful metadata open still leaves normal source/playback checks in place.
