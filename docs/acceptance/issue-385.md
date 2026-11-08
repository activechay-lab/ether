# Issue 385: source-speed adaptation correctness

Local correctness patch for 0.3.6, based on `673aa74cb`. This is not a
throughput benchmark or a completed real-player acceptance receipt.

## Confirmed behavior and changes

The old tracker accepted one 1 MiB / 2.4 second cold read as approximately
436,907 bytes/s. Its 256 KiB / one-second thresholds both passed. The urgent
branch bypassed the configured 20-second grace after six seconds, and the
ordinary branch had no comfortable-buffer veto. Copy compatibility was only a
preference, allowing a lower-bitrate replacement to require video conversion.
Exhausting unsuccessful speed adaptations stopped the current stream.

The tracker now requires four reads, 256 KiB and one second of aggregate read
time. It includes fast reads instead of dropping successfully prefetched/cache
bytes from the delivery numerator. This is **effective source-read delivery**,
not fresh network throughput or a wall-clock arrival rate. The warning names it
`sourceReadBps`; it no longer feeds resolver provider-capacity learning. Cache
bytes must not teach a provider an artificially high network capacity.

Every source-speed trigger honors the full grace. Known buffer runway over
25 seconds defers adaptation. A blocked read still supplies outage evidence
after grace, even without completed reads. Slow delivery does not mark a
release bad.

HLS speed replacements must pass the existing probed `SwapWouldCopyVideo`
contract check on serial, raced and prepared-standby paths, as well as existing
swap admission. Audio adaptation and ordinary failure recovery keep their
existing behavior. Unknown/non-lower bitrates are rejected consistently.
Direct-play representation/remux rules remain in place. Unsuccessful speed
adaptation consumes its bounded attempt budget but leaves the current source
running. Provider pool limits, priorities, race widths and cancellation remain
unchanged.

## Focused offline validation

One selected playback test invocation passed on 2026-11-08 in 3.119 seconds,
with `GOMAXPROCS=2`, `-p 1`, `-count=1` and `-timeout=90s`. No full suite or live
provider was used. Selection:

```
Test(DownFailover|DirectFailoverFallbackRegressions|LowBufferHonoursGrace|BlockedSourceRecovers|PreparedSpeedAlternate|PreparedAlternateIsClosed|SeekSettlingAndPaused|StopCancelsDownFailover|FailoverPrefersProbedCopy|FailoverSkipsReplacement|FailoverRaceSkipsReplacement|FailoverWithOnlyIncompatible|RetryableDirectPlayKeeps|SpeedPosition|BufferRunway)
```

Existing tests cover throttled source reads through HTTP, lower-bitrate swaps,
requested-position mapping, direct-play fallback, candidate compatibility and
stop cancellation. Updated obsolete expectations that required bypassing grace
or stopping after misses. Extended the existing evidence test with cold-read,
cache-delivery and comfortable-buffer cases. Added the complex copy-contract
invariant across serial/raced/prepared candidate acquisition paths.

## Outstanding acceptance and scope

No real Apple TV/Plex flow, QSV conversion flow, live Usenet throughput, or
multi-viewer provider fairness measurement was performed. Provider fairness is
preserved structurally by leaving scheduling untouched; it is not newly
benchmarked. No throughput gain or 80 Mbit/s sustainability is established.

The maintainer comment additionally promised adaptive parallelism, minimum
wall-clock fresh-network delivery accounting including play-ahead, and
per-stream connection/pipeline telemetry. Those are not implemented here.
The reported fixed-concurrency throughput ceiling remains an inference from
reporter measurements. This patch must not be presented as completing those
promises or closing the entire issue.

Real-flow validation remains required before an upstream PR/push under the
owner's acceptance rule. No deployment or release is authorized by this receipt.
