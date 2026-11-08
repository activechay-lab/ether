# PMS baseline and pending Ether comparison

This is preserved measurement evidence, not a release acceptance result. No
Ether comparison has completed as of the 2026-11-08 recovery. The live queue
and its binaries have not been modified by this report work.

## Evidence

- `pms-1k.json` is a byte-for-byte copy of the original `baseline.json`.
- `pms-1k-report.md` is the original generated report.
- `comparison.md` renders all 80 client/step rows, with pending Ether columns.
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
cold 40 ms, warm 18/25 ms. No Ether win/loss conclusion is possible yet.

## Reproduce the comparison without running a benchmark

From the repository root:

```sh
python3 scripts/pms-baseline/comparison.py \
  --pms-1k docs/audits/036-pms-baseline/pms-1k.json \
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

Future corrected runs must measure both clients cold (omit the existing
`-cold-clients plex-appletv` restriction), retain 20 warm samples per step,
and record fixture source/counts and server/harness provenance. Obtain the
coordinator's focused verification and heavy-slot approval before execution.

## Remaining work

Collect actual 54k/540k outputs and compare with the stated limitations. Obtain
C's existing completed Mac fixture for 1.3M work: 1,278,768 titles and 1,697,220
catalogue rows, under old coordinator `w-C/gate-1.3m`. It was not published on
the server at handoff; do not regenerate it. Validate full-row workload and
exact source provenance before any release performance claim. Optional PMS
10k is unmeasured and its old media tree was removed by the previous worker;
it is not part of the live chain. Preserve named containers/volumes until the
coordinator approves eventual lifecycle cleanup. Never claim or sign in PMS.
