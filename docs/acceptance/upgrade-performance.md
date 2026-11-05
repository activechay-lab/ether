# v0.3.5 upgrade investigation

Discord reply for swordofwar (send while the v0.3.4 upgrade is running):

> @swordofwar Please leave the Ether service running; do not stop or restart it while the upgrade finishes. The full “11 of 11” bar is misleading. Ether can keep that label while it converts stored credentials after the schema updates. The most likely remaining step is securing cached release data; the screenshot alone cannot confirm that.
>
> Open `C:\ProgramData\Ether\logs\ether.log`, or `logs\ether.log` under your custom data directory. Look for `store: snapshot taken before applying database migrations`, `credentials: converted stored blobs to v2`, and later `credentials: sealed stored secrets`. `config: converted stored credentials to v2` is an earlier settings-file conversion. These are completion messages, not continuous progress counters; quiet time between them does not itself mean the service has stopped.
>
> If the page and log still have not changed in another **10 minutes**, send the last 100 log lines, the latest log timestamp, and whether Task Manager shows CPU or disk activity for Ether. Remove passwords, tokens, and provider URLs before sharing. Keep the service running while we review that.

The default is `%PROGRAMDATA%\Ether`, falling back to `C:\ProgramData\Ether` (`internal/config/config.go`, DefaultDataDir); `ETHER_DATA_DIR` overrides it. `cmd/ether/main.go` writes `logs/ether.log` and `logs/crash.log`.

The exact v0.3.4 registration order, inspected against databases created by the actual tags, is in [migration-order.txt](upgrade-performance/migration-order.txt). v0.3.3 has **11** pending updates; v0.3.2 has **49**. Both end at **stats/10**, two additive columns on `stats_plays`. The 0.3.3 pending sequence is availability/6, catalog/32, livetv/5, livetv/6, plexguid/4, plexguid/5, plexwatch_pms/1, state/6, state_plexwatch/5, stats/9, stats/10.

The startup order is:

1. Open/rewrite `config.json`, including v2 field sealing. Its `config: converted stored credentials to v2` message is **before** the database opens. It cannot identify a later database row conversion or establish its throughput.
2. Before pending schema changes, create the SQLite safety copy with `VACUUM INTO`; retain its incomplete marker across interrupted upgrades.
3. Apply each migration in its own transaction. Bounded `ANALYZE` of newly created indexes runs inside that migration. catalog/32 creates/backfills release-name indexes; state_plexwatch/5 updates the watch-state clocks in one set-based transaction.
4. Run bounded `PRAGMA optimize=0x10002`, build cluster state, check the credential-key fingerprint, then convert legacy identity/cache blobs. **v0.3.4 retains the final migration label through these steps.** There is no second post-migration VACUUM backup.
5. Change to Starting services; construct components, including the once-only sort-title normalization. Catalogue construction does not perform a full remote catalogue resync or universal FTS rebuild.
6. In Run, upgrade shared settings and component secrets (Trakt, notifications, ratings, IPTV source JSON and indexer cookies); purge legacy search cache; finish the startup WAL checkpoint before Ready. Routine integrity checking and catalogue synchronization run in the background later; restored backups have an explicit pre-service integrity check.

Three scaling defects were confirmed:

- The blob conversion selected distinct payloads and updated by payload value without an index: repeated full-table scans, effectively quadratic for unique cached releases. It now updates by the complete primary key, retaining a compare-and-swap predicate.
- Source, token, notification and cookie conversions committed each changed row separately. They now commit every **500 writes**, retaining unchanged-value predicates and returning write/sealing errors. No durability settings were weakened.
- After older catalogue migrations, SQLite could select `catalog_titles_type_browse_show_added` for the release-name keyset query and sort the matching catalogue for **every 500-row page**. This was observed on the 0.3.2 fixture. The query now ranges only on the primary key and filters types in Go, visiting every title once. A batch of episodes cannot prematurely end the traversal.

Committed credential batches survive interruption; sealed v2 values are skipped on restart. A failed batch rolls back its tail. Schema/index migrations remain atomic and restart from their migration boundary; release-name traversal is bounded and fast enough to retry. Sort normalization already commits batches of 2,000 and skips normalized rows on retry; its completion marker is written last. The focused batch test verifies rollback of an uncommitted tail and preservation of completed rows on retry. An interrupted 0.3.2 measurement was also restarted successfully with the original safety snapshot retained.

Measurements on this Mac, 2026-11-05 (synthetic data, no real credentials):

- 600,000 titles with 1 KB summaries, 1,000,000 watch-state rows, 1,000,000 history rows, 1,000,000 play records.
- 42,000 IPTV sources (two credential-bearing URLs each), 42,000 channels, 42,000 Trakt token pairs and 42,000 distinct cached feed releases. An additional pass seeds 42,000 configured indexer-cookie rows, 42,000 webhook configurations and a ratings API key.
- The all-movie catalogue stresses the maximum release-name workload. A mixed 0.3.2 pass uses 42,000 shows, 100,000 movies and 458,000 episodes linked to those shows.
- Database schema/backup measurements use the native SSD. Credential and sort passes use SQLite WAL/FULL plus **10 ms added per explicit commit or automatic write** through a test-only driver. This models expensive durability boundaries, not Windows NTFS, Defender, disk bandwidth, or an end-to-end production boot. SQL and backup timing on a slower disk can still be longer.

| Step | v0.3.4, native SSD | v0.3.5, 10 ms commit delay where noted |
| --- | ---: | ---: |
| 0.3.3 backup and migrations | 20.89 s | 16.11 s (native) |
| 600k release-name backfill | 4.15 s | see per-migration logs |
| 1m watch-state clock update | 0.84 s | 0.56 s (native) |
| 42k cached credential blobs | **197.79 s** | **1.45 s** |
| 42k IPTV sources | 6.46 s | **1.49 s** |
| 42k IPTV sources with 10 ms added per durability boundary on both versions | **575.38 s** | **1.49 s** |
| 42k Trakt token pairs | 5.55 s | **1.23 s** |
| 42k configured indexer-cookie rows | — | **1.22 s** |
| 42k webhook configurations | — | **1.35 s** |
| Ratings settings | — | 0.011 s |
| 600k sort-title normalization, marker absent | not included in old run | **29.03 s** |
| Final checkpoint | 0.013 s | 0.012 s |
| Full measured database path, including sort and additional credentials | old subset 231.38 s | **52.86 s** |
| 0.3.2 backup, 56 migrations and primary credential conversions | — | **26.10 s**, before optional sort/accessory pass |
| Complete mixed 0.3.2 workload, including sort/cookies/webhooks | — | **52.87 s** |

Every migration's elapsed time is retained in [v035-from033.txt](upgrade-performance/v035-from033.txt) and [v035-from032.txt](upgrade-performance/v035-from032.txt). A repeat with separate phase timing is in [v035-phase-timings.txt](upgrade-performance/v035-phase-timings.txt): settings conversion 13 ms, final optimization 382 ms, and bounded migration statistics calls mostly single-digit milliseconds (up to about 31 ms in the watch-state tail). This warmer repeat completed in 37.42 s. Statistics intervals include the ensuing transaction commit and are not raw SQL-only timings. These include migration-local index/statistics work; their totals should not be added a second time. The release branch has seven extra migrations beyond v0.3.4, hence 18/56 instead of 11/49. The mixed catalogue run is in [v035-mixed-from032.txt](upgrade-performance/v035-mixed-from032.txt). The full old source run with added commit delay is in [v034-slow-commits.txt](upgrade-performance/v034-slow-commits.txt): 9m35s versus 1.49s for the batched path (the sleep is at least 10 ms; scheduler/SQL overhead adds to it). That standalone source comparison reopened an already migrated schema; its schema-ahead warning concerns unrelated release-branch migrations. The original v0.3.4 subset is in [v034-native.txt](upgrade-performance/v034-native.txt). The first provisional 29.5 s result omitted sort normalization and the additional cookie/webhook pass; **52.86 s supersedes it** for the complete measured all-movie workload.

The page now names actual operations, exposes per-step elapsed time and row progress, estimates remaining time only after measurable work, and uses an indeterminate bar for SQL without a row counter. Total elapsed time continues advancing between polls. All 16 new messages are translated in all 20 shipped locales. Generic schema operations include their component/version. Fixed task IDs and counts carry no secrets. Progress logs use `startup: upgrade progress`, `task`, `done`, and `total` at the start, finish and every 5,000 rows.

Screenshots are the real Vite startup component with simulated status responses, not captures of a live customer upgrade: [credentials](upgrade-performance/credentials.png), [release names](upgrade-performance/release-names.png), [database statistics](upgrade-performance/statistics.png).

Reproduce with the opt-in package harness (excluded from normal builds by `upgradebench`):

```sh
# In an isolated checkout of v0.3.3 or v0.3.2, copy
# internal/app/upgrade_fixture_test.go from this branch, then:
ETHER_UPGRADE_FIXTURE=/tmp/ether-upgrade-base CGO_ENABLED=0 \
  go test -tags upgradebench ./internal/app -run '^TestUpgradeFixtureManual$' -v -count=1
# Add ETHER_UPGRADE_MIXED=1 for the show/movie/episode workload.
# Copy the completed data directory to a disposable measurement directory.
# In this branch:
ETHER_UPGRADE_MEASURE=/tmp/ether-upgrade-measure ETHER_UPGRADE_ACCESSORIES=1 \
  CGO_ENABLED=0 go test -tags upgradebench ./internal/app \
  -run '^TestUpgradeManual$' -v -count=1 -timeout 5m
```

The optional `ETHER_UPGRADE_ONLY` selects a named post-schema step. The v0.3.4 comparison used the same basic harness against the tagged implementation; remove newer progress-name/context APIs and the newer catalogue startup-context option when copying it to the old tag. No fixture or real data directory is committed.

Validation: focused Go checks passed in store, startup, app, catalog, trakt, livetv, notify and cardigann; web TypeScript passed; 47 focused startup/i18n checks passed; all locale catalogues are complete. An initial pnpm script invocation unexpectedly ran the full web suite: 95 files passed, 14 failed with a worker timeout and other failures. That broad run is not claimed as a gate; the corrected direct Vitest command limits workers and selects only the relevant three files. No full Go build/suite or race suite was run.
