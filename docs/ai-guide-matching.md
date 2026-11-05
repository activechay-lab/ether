# AI-assisted guide matching (0.3.5)

In Live TV → Guide, choose **Match with AI** beside Auto-match. Ether shows an
estimated provider-call count and approximate input tokens before you run it.
The shared connection is configured under **Integrations → LLM**; no additional
provider settings are introduced. Auto-match continues to work without AI.

Ether first runs its existing deterministic matcher. Only unresolved IPTV
channels enter the AI pass. Guide candidates come from the same persisted
XMLTV, Xtream and Plex guide metadata. Fuzzy retrieval considers names, aliases,
region, numbers, HD/SD and programme-title overlap where known. Explicit region,
number and timeshift conflicts are excluded. Each channel gets at most six
candidates; the model receives names and bounded now/next titles, with local
candidate indices instead of source credentials or stream addresses.

Proposals include confidence and a reason. Confidence of at least 95% is selected
only when the region is known; inconsistent numeric identity is capped below
that threshold. Confidence is a model estimate, not a measured probability.
Owners can select lower-confidence suggestions themselves. Apply accepts only
selected channels and fingerprinted proposals. Changed channel or guide inputs
require another review. Existing streams keep playing.

## Limits and caching

- Eight channels per batch, at most 32 provider requests per run.
- 18 KB maximum batch JSON; 120,000 approximate input tokens per run.
- 2,048 output tokens per request, 20 seconds per request, 90 seconds per AI pass.
- Bounded guide calls do not retry automatically or negotiate request options.
- Provider prices differ; Ether shows calls/tokens rather than inventing a dollar price.
- Decisions, including abstentions, persist in SQLite/PostgreSQL under a SHA-256
  fingerprint of channel state and shortlisted guide identity/content.
- Cached decisions cost no provider calls. Unprocessed channels are counted as
  deferred; Continue opens another estimate before any additional provider work.
- Concurrent runs in one service are serialized/rejected; caches persist across
  restarts. Separate cluster nodes can still initiate simultaneous uncached work.

The existing POST `/livetv/guide/match` accepts `ai: estimate | propose | apply`,
optional `guideSourceId`, and, for apply, selected `channelIds` and `proposalKeys`.
Estimate and propose force dry-run behavior. The owner route marks explicit
selections reviewed. MCP and Ask Ether expose `livetv_guide_match`; paid work and
Apply go through the existing owner approval mechanism, with cost or mapping
details on the approval. Read-scope MCP clients cannot call the write tool.

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

## Verification and gallery

Scoped gates passed:

```text
ok internal/livetv
ok internal/llm
ok internal/agent/builtin
ok internal/mcp
ok internal/app (full package earlier; final MCP workflow check below)
GUIDE_ROUTE_TOOL_COVERAGE PASS: POST /guide/match → livetv_guide_match (owner approval required)
web typecheck: passed
web changed-file/i18n tests: 3 files, 53 tests passed
20 locales complete and valid
```

This release baseline does not contain a general route-table-to-MCP coverage
gate. The existing MCP/app catalogue checks were updated for the added tool and
the app workflow now checks the guide endpoint and owner-scoped tool together.
No full Go suite, race suite, full web suite or production build was run.

Owner gallery: `output/playwright/ai-guide-matching/index.html` (local artifact),
with eight screenshots: estimate/review at 1440×900 and 390×844, dark/light.
Screenshots use the actual UI with the offline mock backend. All mobile proposal
names wrap, and the page has no horizontal overflow at 390 pixels.

Discord reply draft:

> Hey Kakasanti — yes! We’ve built AI-assisted guide matching for 0.3.5. It uses
> your existing AI connection to suggest matches for channels auto-match misses,
> including Plex guides. You’ll see confidence and a short reason, then choose
> which matches to apply. It’s ready for owner review before release.
