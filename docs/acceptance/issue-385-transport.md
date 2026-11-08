# Issue 385: recovered transport slice

Local recovery on `recovery/issue385-native-delivery`, based on `673aa74cb`.
All ten original dirty/untracked files were retained and revised; the untouched
recovery snapshot remains in parent thread storage (`native-385-preserved`).
The prior Claude transcript was inspected narrowly. Its contributor hypotheses
about fixed concurrency and per-connection speed are not measured conclusions.

## Implemented

An additive `core.SourceDeliveryReporter` capability exposes decoded bytes,
successful fetch completions, union busy wall time, outstanding fetch calls,
current speculative budget, recent per-fetch completion rate, and normal
read-ahead pipeline depth ceiling. Demand, near read-ahead, and disk play-ahead
use the same meter. Engine hot/disk cache hits and joined engine flights do not
increment bytes. Errors contribute busy time but not delivered bytes.

Use delta bytes / elapsed snapshot wall time. Busy time excludes idle gaps and
includes source/provider queue waits; it is not the throughput denominator.
Bytes count successful decoded fetch returns before stream normalization, not
verified unique playable bytes. Fetcher coalescing can share underlying wire work,
and hedges/retries can deliver duplicate articles. Do not sum these counters as
unique provider traffic, interpret outstanding fetches as held connections, or
teach resolver capacity from them. Pipeline depth is a normal policy ceiling,
not actual occupancy; background depth is one and repair bursts can be larger.
Engine flights attribute work to their originating source, not every joiner.

Adaptive near read-ahead estimates work needed for 1.5 times media byte rate
using fetch-completion EWMA (including queue wait). It is capped at 64 and a
75% primary-capacity share across recent/outstanding readers. Known small
capacity can reduce the configured base; unknown capacity never grows above it.
A minimum one work item preserves progress on small pools. Near and disk
read-ahead share a cancellable admission budget immediately before fetching;
disk work may occupy at most half (minimum one). Demand bypasses this gate.
Existing provider scheduling, reservations, priorities and account/node limits
remain authoritative. Shrinking budgets drain rather than abort active work.
This is bounded work admission, not a demonstrated multi-viewer fairness guarantee.

The recovered NNTP pacing fixture and opt-in measurement are retained. The
measurement now uses 8 MiB per title rather than 128 MiB, exact observed warmup
offset, a 30-second context, and distinct reader and decoded-fetch rates.
Pacing waits stop when the fake server closes, and short writes fail explicitly.

## Evidence from this recovery

With `GOMAXPROCS=2 CGO_ENABLED=0`, `-p 1 -count=1 -timeout=60s`, selected
stream tests passed in 0.640s: `TestDeliveryAccounting`,
`TestDeliveryBudgetAndCancellation`, `TestPlaybackReadAheadStopsOnClose`,
`TestPlaybackReadAheadNeverDelaysDemandReads`,
`TestSamePriorityWaitersCoalesceAndCancelIndependently`, and
`TestCancelledLaneRemainsBoundedUntilWorkerExits`.

One opt-in loopback case ran in 4.314s with a 45-second test timeout:
`ETHER_MEASURE_USENET=1`, test
`TestMeasureLatencyBoundStreamThroughput/two_streams,_24_connections`.
Two 8 MiB titles, 64 KiB articles, 150 ms reply latency, and 500,000 encoded
bytes/s per fake connection produced:

| Stream | Overall reader MB/s / target fraction | Post-warmup reader MB/s | Decoded fetch completion MB/s |
| --- | --- | --- | --- |
| 0 | 2.58 / 0.26 | 2.50 | 3.57 |
| 1 | 2.38 / 0.24 | 3.14 | 3.39 |

The normal pipeline ceiling was two. Final adaptive budgets were 9 and 18
(the first source had closed by the latter snapshot). The server recorded 273
articles and maximum 26 accepted handler lifetimes for the configured 24-slot
pool. That fixture count includes handler teardown; it does not prove the
provider cap or live-connection fairness. No baseline comparison was run.
The configured aggregate raw ceiling is 24 × 500,000 = 12,000,000 bytes/s
before reply latency and encoding/framing overhead. The two nominal 10 MB/s
targets total 20 MB/s and cannot both be met by this fixture. Missing that
target is not regression evidence; increasing pools to satisfy it would change
the experiment. The 1 MiB warmup and 8 MiB titles are too short to establish
steady state. No raw-wire rate is measured by decoded-fetch counters.

The tiny synthetic transfer does **not** sustain the 10 MB/s target, establish
any speed improvement, or substitute for a real provider/player acceptance.

## Integration and remaining acceptance

Compose with local `74e31927a` (playback source-speed/failover correctness).
There are no overlapping implementation files or acceptance document paths.
That patch's cache-inclusive `sourceReadBps` remains separate; this slice does
not change playback triggers or restore resolver capacity learning. The parent
must verify the composed result. No push, upstream PR, deployment or release
was performed or authorized by this receipt.

Real Apple TV/Plex flows, sustained high-bitrate playback, actual per-stream
socket occupancy, provider fairness under multiple viewers, and a controlled
before/after throughput comparison remain unverified. No live providers,
account tokens, full suites or heavy benchmark were used. Issue 385 is not
closed by these local correctness and synthetic measurement results.
