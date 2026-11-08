# PMS baseline and pending Ether comparison

This is preserved historical measurement evidence, not a release acceptance
result. The existing Ether 54k run finished on 2026-11-08 at 16:12 UTC and
540k at 17:39:59 UTC; both comparisons are included. Their chain finished at
17:40 UTC. The queue and its binaries were not modified by this report work.
Ether 1.3M remains unmeasured.

## Evidence

- `pms-1k.json` is a byte-for-byte copy of the original `baseline.json`.
- `pms-1k-report.md` is the original generated report.
- `comparison.md` renders all 80 client/step rows for PMS 1k and historical
  Ether 54k/540k, with the 1.3M column pending.
- `ether-54k.json` and `ether-54k-report.md` preserve the original summary and
  report bytes. `ether-54k-terminal.log` preserves its terminal output.
  `ether-54k-evidence.json` records source artifact hashes, the observed exit
  marker, coverage, and the prior dirty-binary observation. Raw requests remain
  at `/data/036-work/i/ether-036-pms-work/runs/ether-54k/results.json`.
- The equivalent `ether-540k` JSON/report/terminal/evidence files preserve the
  completed larger run. Its raw requests remain in the sibling
  `runs/ether-540k/results.json`. `chain-complete.log` preserves both exit
  markers and `chain done`. Evidence manifests include SHA-256 hashes.
- Original directory on bb-worker-server:
  `/data/036-work/i/ether-036-pms-work/runs/pms-1k/`.
  Preserve `results.json` there for individual request observations.
- Pinned PMS image and reported version: `plexinc/pms-docker:1.43.5.11079-b3b13e506`.
  Original identity records `claimed=0`. The historical artifact has no image
  digest, binary hash, or harness source manifest; do not infer those retroactively.

The library has 700 movies, 300 shows, 915 seasons, and 8,686 episodes. The old
worker records offline embedded movie title/year/summary/genres plus artwork;
metadata is not equivalent to a fully matched production library. Movie page
bodies here are about 76 KB for 60 items. Deep movie paging returns zero items
at this size. Empty watch-history/postplay and playlist responses cannot be
compared as equal work against populated Ether fixtures. Live TV returns
403/404 and is excluded from performance comparisons.

Apple TV has 40 cold steps and 20 warm rounds. Web has 20 warm rounds but **no
cold measurements**. A zero cold duration in the JSON means not measured.
The old run evicted server files between restarts; this is not a whole-host
cache reset. CPU covers the whole container, including background work; block
writes are charged at writeback. Gzip is locally recompressed response size;
wire size is separately recorded. Host load varied during the PMS run; timing
comparisons must retain that caveat.

Useful PMS Apple TV measurements: home `/hubs` cold 310 ms, warm p50/p95
14/15 ms; `/hubs/search star` cold 1,489 ms, warm 74/82 ms; first movie page
cold 40 ms, warm 18/25 ms. The historical comparison below is limited to
observations from these workloads; it does not establish release superiority.

## Historical Ether 54k result

The chain records `ether-54k exit 0`, and all three output artifacts exist.
Raw request inspection establishes 1,800 records: 80 discovery, 40 Apple TV
cold, 80 priming, and 1,600 warm requests (20 for every step/client). Every
record has probe data. No request was skipped or failed to connect. There are
1,665 HTTP 200 responses and 135 HTTP 404 responses; all 404s are the three
unsupported Live TV steps, across discovery (6), cold (3), priming (6), and
warm (120). Thus 74 of 80 summary rows are HTTP 200; the six Live TV rows are
not passing coverage. Neither server has Web cold measurements.

Selected Apple TV observations from the raw summary, in milliseconds:

| Step | PMS cold | Ether cold | PMS warm p50 / p95 | Ether warm p50 / p95 | Comparability |
|---|---:|---:|---:|---:|---|
| Home `/hubs` | 310.656 | 394.870 | 14.125 / 15.809 | 3.487 / 3.882 | 8 vs 10 hubs; 26,282 vs 175,567 response bytes |
| `/hubs/search star` | 1489.430 | 80.783 | 74.494 / 82.988 | 22.852 / 29.812 | 17 items each, but 62,880 vs 31,528 bytes; relevance equivalence unproven |
| First movie page | 40.274 | 230.367 | 18.054 / 25.807 | 23.004 / 29.225 | 60 items each; total movies 700 vs 24,969 |
| First episode page | 73.882 | 57.305 | 70.678 / 79.002 | 33.252 / 38.124 | 100 items each; total episodes 8,686 vs 23,937 |
| Movie detail | 27.146 | 480.765 | 11.996 / 16.642 | 7.705 / 9.143 | 1 item each, but 15,296 vs 2,816 bytes |

Ether was faster for warm home/search and the first episode page in this run;
the first movie page and several cold paths were slower. These data do not
isolate why: library contents, payload work, caches, and host load differ.
Deep movie paging is not comparable (PMS returns 0 items; Ether returns 60).
No causal performance explanation or clean release-SHA claim follows.

Ether's recorded host load fell from 7.53/9.88/13.54 to 2.73/3.98/7.27;
post-warm memory was 612 MiB (peak 616 MiB). Its Plex identity version is a
protocol identity, not Ether source provenance. The prior dirty server/harness
revisions below still apply as historical observations; no signed-off binary
manifest or before/after binary hashes exist for this run.

## Historical Ether 540k result and terminal state

The chain records `ether-540k exit 0` followed by `chain done`. The terminal
log names the completed report, and report/summary/raw artifacts are present.
Independent inspection of the existing data found the same exact coverage as
54k: 1,800 raw records, 80 unique client/step pairs, 20 warm samples for every
pair, one discovery/priming sample per pair, and one cold sample for each
Apple TV step. Web has no cold samples. All records have probe data; there
are no skips or transport failures. HTTP coverage is 1,665 responses at 200
and 135 at 404. Each unsupported Live TV step contributes 45 errors across
phases/clients. These errors remain excluded from performance acceptance;
the process exit code does not erase them.

Selected Apple TV 540k measurements:

| Step | Cold ms | Warm p50 / p95 ms | Items / total | Response bytes |
|---|---:|---:|---:|---:|
| Home `/hubs` | 134.746 | 3.726 / 4.006 | 10 hubs | 169,536 |
| `/hubs/search star` | 3182.531 | 67.736 / 83.487 | 17 items | 51,520 |
| First movie page | 255.899 | 23.545 / 31.137 | 60 / 249,760 | 87,737 |
| First episode page | 18989.301 | 33.561 / 43.741 | 100 / 239,558 | 178,851 |
| Movie detail | 278.990 | 7.763 / 10.055 | 1 item | 2,818 |

Warm home and episode-page times remain low, but cold episode paging took
18.99 seconds and cold search 3.18 seconds. The data do not support a general
performance victory: cold costs, payload differences, relevance, and metadata
completeness still matter. Post-warm memory was 2,492 MiB, peak 2,807 MiB;
initial idle CPU was 339.7 ms/s. Host load changed from 2.64/3.59/4.21 to
3.71/4.28/4.82. Warm rounds took 76.397 seconds. The same dirty-source and
missing before/after binary-hash limitations apply; this is historical only.

Collection confirmed PIDs 3687147, 3687149, 3933523 and 3996324 absent, with no
remaining matching I measurement process. All three named containers were
stopped with exit 0. Old thread `thr_h2m72vcaxu` has zero queued messages and
zero active background agents. No live I job dependency remains. Both worker
threads are ready for coordinator-managed archival after this evidence commit
is collected. Preserve the unmanaged checkout `env_ars9qn7fdp`, its branch,
all original outputs under `/data/036-work/i/`, and the old thread history;
no container/volume cleanup or archive was performed by this collection.

## Reproduce the comparison without running a benchmark

From the repository root:

```sh
python3 scripts/pms-baseline/comparison.py \
  --pms-1k docs/audits/036-pms-baseline/pms-1k.json \
  --ether-54k docs/audits/036-pms-baseline/ether-54k.json \
  --ether-540k docs/audits/036-pms-baseline/ether-540k.json \
  > docs/audits/036-pms-baseline/comparison.md
```

When real results arrive, add `--ether-54k PATH/baseline.json`,
`--ether-540k PATH/baseline.json`, and eventually `--ether-1-3m PATH/baseline.json`.
The renderer hashes each supplied artifact and checks rows for missing samples,
errors, and unsupported HTTP responses. Its success is only successful report
generation: acceptance still requires inspecting raw `results.json` (including
cold statuses/errors), coverage, counts, provenance, and workload comparability.
Do not interpret a chain `exit 0` or generated report as acceptance.

Existing outputs remain under `/data/036-work/i/ether-036-pms-work/`:
`chain.log`, `runs/ether-54k.log`, `runs/ether-540k.log`, and eventually
`runs/ether-54k/` and `runs/ether-540k/`. The preserved chain runs 54k then
540k through the machine-wide heavy lock. Do not enqueue a duplicate.

## Source provenance correction for future runs

Read-only build-info inspection during recovery found:

| Binary in preserved work directory | Embedded revision | Modified |
|---|---|---|
| `bin/ether` | `82cd556aef0cbcf96beb347280b9682205fb1bfd` | true |
| `bin/etherperf` | `8bbd73c139a3add2d417441f1ba0e462d9b82efb` | true |

These are observations of the surviving binaries, not proof of what ran in
the earlier PMS measurement. They also mean the queued Ether measurements
cannot certify a clean release SHA. Keep those results as historical evidence.
The checkout SHA at recovery was `bc0a0fcb6ec271b810afdf674be05fc11180953a`;
checkout HEAD alone does not identify either executable.

For a future authorized benchmark, build from clean committed source into a
new directory outside the checkout and outside the live chain's paths. Keep
server and harness expected full SHAs separately; preserve Go toolchain/build
settings. Use Go VCS stamping and do not override dirty state with linker labels.
Do not replace the existing binaries or repoint the existing containers.

`scripts/pms-baseline/provenance.py` prepares a read-only preflight manifest:

```sh
python3 scripts/pms-baseline/provenance.py \
  --go /data/bb/toolchains/go1.27.3/bin/go \
  --server-binary /data/036-work/i/FUTURE/bin/ether \
  --server-sha EXPECTED_FULL_SERVER_SHA \
  --harness-binary /data/036-work/i/FUTURE/bin/etherperf \
  --harness-sha EXPECTED_FULL_HARNESS_SHA \
  --container FUTURE_NAMED_ETHER_CONTAINER > provenance-before.json
```

This rejects missing VCS metadata, either dirty binary, revision mismatch, and
a container that does not directly execute the inspected read-only mounted
server. It records binary SHA-256, toolchain/build settings, container ID, and
immutable image ID without capturing environment variables or viewer tokens.
It does not build or start anything. A failed preflight is a hard stop for the
future measurement. Use immutable build paths and the exact inspected harness
path; retain a second manifest after measurement and require unchanged binary
hashes and container/image identity. A preflight alone is not certification.
The check is prepared separately and is not installed into the live chain.

Focused enforcement verification passed on 2026-11-08: five Python unittest
methods cover a clean control plus dirty, missing, and wrong-revision metadata
for both server and harness, and six mount mismatches (source, writability,
mount type, destination, absence, and duplicate destination). All external
commands are mocked; fixtures are tiny non-executable files. No Docker or Go
process is invoked. This establishes the rejection logic, not an end-to-end
benchmark or release-head certification. Reproduce with:

```sh
TMPDIR=/data/036-work/i/tmp PYTHONDONTWRITEBYTECODE=1 \
  python3 -m unittest discover -s scripts/pms-baseline -p test_provenance.py -v
```

Future corrected runs must measure both clients cold (omit the existing
`-cold-clients plex-appletv` restriction), retain 20 warm samples per step,
and record fixture source/counts and server/harness provenance. Obtain the
coordinator's focused verification and heavy-slot approval before execution.

## Remaining work

Both queued measurements are collected; there is no remaining I benchmark
job. The original release requirement remains unfinished: obtain
C's existing completed Mac fixture for 1.3M work: 1,278,768 titles and 1,697,220
catalogue rows, under old coordinator `w-C/gate-1.3m`. It was not published on
the server at handoff; do not regenerate it. Validate full-row workload and
exact source provenance before any release performance claim. Optional PMS
10k is unmeasured and its old media tree was removed by the previous worker;
it is not part of the live chain. Preserve named containers/volumes until the
coordinator approves eventual lifecycle cleanup. Never claim or sign in PMS.

## Integrated harness commands and raw artifacts

Use `etherperf baseline-fixture` for the container-oriented fixture builder
(`OUT/data` plus `OUT/viewer.json`, with `-from` to reuse a snapshot).
`etherperf fixture` retains the gate builder's direct data-directory layout.
Existing queued runs retain their original binaries and commands.

New raw `results.json` records carry `schemaVersion: 2`: `items` is the
numeric Plex container size in measurement mode; `renderedItems` maps title
types to counts for the gate's page bound. Older gate records used an object
in `items`, while older baseline records used a number. Raw consumers must
distinguish those legacy forms by JSON type; do not treat absent new fields
as proof of empty results. Existing evidence is preserved without rewriting.
`baseline.json` summary rows and comparison-tool inputs are unchanged.
