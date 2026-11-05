# AI-assisted guide matching (0.3.5): owner review revision

The original inline AI UI was rejected. AI matching now opens a stacked review
**drawer** from one button beside Auto-match in the Channel mapping controls.
There is no AI button in the page hero. The same launcher inside a channel’s
guide drawer opens review above it; Change guide opens a third drawer. Escape,
focus trapping and scrolling follow the console’s existing Drawer component.

## Reviewing large lineups

The drawer starts with already-matched, unmatched, high-confidence and
needs-review counts, plus an upper bound for provider calls and time before
running. Matching runs in batches of eight with visible progress. Pause aborts
the in-flight request; Resume starts only unfinished channels. Each run stops
after 32 batches (256 channels), preserving the rest for the next run.

The fixed-height virtual grid mounts only the rows around the viewport. Search,
confidence, source, region, group and status filters compose with name,
confidence, source and status sorting. Bulk actions accept high-confidence
proposals or accept/reject the full filtered set, including off-screen rows.
Rows offer Accept, Reject and Change; reasons appear in a compact popover.
Focus the grid and use j/k (or arrow keys) to move, a to accept, r to reject.

Review drafts persist in this browser tab’s session storage through drawer
closure and page reload. Only mapping/review metadata is stored; no stream
addresses or provider credentials. Server-side decision caching persists across
restarts. Model confidence is an estimate, not a calibrated probability.

Apply sends the accepted set in one request and commits it in one transaction.
AI choices are fingerprint-validated again; manual corrections must refer to a
real stored guide channel. Mapping conflicts abort the entire transaction.
The owner receives a durable undo receipt, valid for 24 hours. Undo atomically
restores the last applied set only when its mappings still match, and preserves
unrelated owner edits. A newer Apply replaces the Undo button’s current receipt.

The existing owner-only POST `/livetv/guide/match` now additionally supports
`ai: commit` with explicit `selections` and expected old mappings, or `ai: undo`
with the returned `undoToken`. Existing estimate/propose/apply clients and MCP
coverage remain compatible. No new routes or provider settings are introduced.
XMLTV, Xtream and Plex guide sources still share the same candidate pipeline.

## 5,000-channel browser measurements

The opt-in mock fixture uses 5,000 IPTV rows: 1,000 existing mappings and 4,000
cached proposals, with 3,000 high-confidence and 1,000 needs-review proposals.
Open `/ether/livetv/guide-data?guideReviewFixture=5000&guideReviewResults=1`
under the mock dev server to reproduce the populated state. Omit
`guideReviewResults` for an empty queue. This fixture is compiled out of the
production bundle.

| Warm host Chrome measurement | 1440×900 desktop | 390×844 phone viewport |
| --- | --- | --- |
| Click event → first result-row paint | 32.8 ms | 35.1 ms |
| Maximum mounted rows while scrolling all 5,000 | 11 | 10 |
| Scroll frame median, 90 samples | 16.7 ms | 16.7 ms |
| Scroll frame p95 | 17.2 ms | 17.3 ms |
| Maximum scroll frame | 19.1 ms | 19.0 ms |
| Horizontal overflow | none | none |

Render timing uses a click listener and MutationObserver followed by the first
animation frame. It excludes page loading, automation scrolling to the launcher
and the existing drawer entrance animation. Scroll sampling spans the full list
after the drawer settles. These are viewport simulations on this Mac, not
physical-phone benchmarks. Live-provider latency and accuracy remain unmeasured.

The browser also verified confidence filtering, keyboard acceptance, manual
changes in a third drawer, Apply and Undo while the original guide drawer stayed
open. Running captures use an explicit 180 ms fake network delay per batch.

## Scoped gates

```text
ok internal/livetv
ok internal/agent/builtin
ok internal/mcp
GUIDE_ROUTE_TOOL_COVERAGE PASS: POST /guide/match → livetv_guide_match (owner approval required)
ok internal/app (targeted MCP workflow)
web typecheck: passed
web changed-file and i18n tests: 4 files, 56 tests passed
20 locales complete: 6434/6434 messages per translated locale
prettier changed files: passed
```

New offline tests cover atomic rollback, undo preserving unrelated edits,
5,000-row virtualization, keyboard and filtered review, one-request Apply,
Pause/Resume without resending finished batches, and restoring undo after reload.
No full Go suite, race suite, full web suite or production build was run.

## Owner gallery

`output/playwright/ai-guide-review-v2/index.html` is the replacement gallery:
30 screenshots at both requested sizes, dark/light, including the 5k summary,
review, filters, running/paused states, nested review/change, reasons, and Undo.
Raw timing data is alongside it in `measurements.json`. The old gallery is
retained for comparison and is not the approval target.

## Offline measurement

Reproduce with `go test ./internal/livetv -run TestGuideAIMessyMeasurement -v`.
The fixture generates 640 IPTV rows using 64 real station brands, ten regions,
and distributor-style RAW/VIP/HEVC/backup variants, against a 3,200-channel XMLTV
with HD, SD, timeshift and international variants. It is synthetic, not a capture
of a customer lineup. An independently labelled oracle fake chooses a canonical
station only when retrieval includes it.

| Metric | Result |
| --- | --- |
| Deterministic matches | 128 / 640 (20%) |
| AI additions, first run | 256 |
| Deferred after first run | 256 |
| First-run provider calls (fake) | 32 |
| First-run AI latency (fake) | 2.351 seconds |
| Precision on first-run labelled AI subset | 256 / 256 (100%) |
| AI additions after continuing | 512 total |
| Total fixture coverage after continuing | 640 / 640 |
| Fully cached rerun | 0 provider calls |

This measures retrieval, budgets, validation and review/apply plumbing. It does
**not** establish live-model semantic precision or network latency. No live run
was made: no LLM configuration was found in the standard local Ether config
locations inspected on this machine. Provider response quality remains an
owner acceptance check with a representative real lineup.

## Release integration

Merged `origin/release/0.3.5` at `2259c6f7d06a3efead9f4c901ec5ba477f835e12`,
preserving the On Air service wiring, personal scheduling, look tools and mock
behaviour alongside AI guide review. On Air owns Live TV migration 7; the AI
cache and undo migrations are now 8 and 9. Catalogues were merged key by key
and synchronized. Full scoped tests for livetv, app, mcp and agent/builtin,
101 web/i18n tests across eight files, and the typecheck pass. All 20 locales
are complete at 6,689 messages.
