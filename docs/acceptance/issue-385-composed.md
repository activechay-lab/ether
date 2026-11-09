# Issue 385: composed offline review

Base: `673aa74cb`. Recovered `74e31927a` applied as `382755a7e` and
`6ff2d7a7e` as `d2a64794d`, without conflicts. Parent retains original backups.
The subsequent local review commit contains this receipt and the correction below.
Read root `design.md` and supplied AGENTS guidance; `docs/design.md` and a
repository AGENTS file are absent. No live credentials/providers/services used.

## Confirmed defect corrected

An HLS speed replacement rejected before changing the input returned an error
without incrementing `downMisses`. Consequently the monitor could keep trying
beyond `MaxDownFailovers`. Count that failed handoff, exclude its candidate for
this session, and retain the original generation. The regression verifies one
attempt consumed, original retained, no bad-release marking, and the monitor
stopping adaptation at the configured cap. Existing rollback and stop handling
remain: if a changed input cannot be rolled back, stopping still prevents use
of a revoked input; retaining the original is not a promise to mask that failure.

## Source review

- Speed evidence includes fast/cache reads, requires multiple completed reads,
  and honors start/seek grace. Blocked reads supply evidence after grace;
  known comfortable runway vetoes a swap. Copy safety is enforced on serial,
  raced and prepared HLS candidates. Exhausted misses retain the original.
- Decoded-fetch telemetry is separate from playback source-read timing. Cache
  hits and engine joiners do not increment it; ownership follows the originating
  flight. Fetcher coalescing/retries preclude interpreting summed bytes as unique
  traffic. Neither metric establishes raw-wire bandwidth or trains resolver
  provider capacity here.
- Demand bypasses source admission, with existing priority-separated flights
  and bounded live hedging. Near/disk work shares the source gate; disk occupies
  at most half (minimum one). Admission is bounded to 64 or a 75% capacity share;
  unknown capacity stays at the configured base (clamped to 64). Shrinks drain.
- Gate waits select cancellation; engine-owned flight contexts cancel on engine
  shutdown or the last waiter leaving. Shared flights survive one waiter leaving.
  Source close cancels its waiters and joins its scheduling work; engine close
  joins flight workers. Meter ownership can outlive a closing source for shared
  work, so it is attribution, not current socket occupancy.
- Equal estimated shares are not a fairness guarantee: minimum-one admission
  may exceed aggregate capacity with many readers, already admitted work drains,
  and admission wakeups are not FIFO. Provider scheduling remains authoritative.
  External Engine.Prefetch and pre-open probes are not source-meter work.

## Focused results

Ballast checked before builds: WATCH, about 5.4–5.6 GB headroom. Used existing
Go cache, `GOMAXPROCS=2 CGO_ENABLED=0`, `-p 1 -count=1`. Initial sandbox attempt
could not write Go cache; authorized retry succeeded. No broad suite, race
matrix, throughput fixture, browser, or long-running service was started.

Composed focused selection passed: playback 3.268s, stream 1.155s (60s test
limit). Selection covered DownFailover, direct fallback, grace, blocked source,
prepared alternate cleanup, seek/pause, stop cancellation, copy preference and
compatibility, direct-play retention, speed position/runway, delivery accounting
and cancellation, disk play-ahead close/demand priority, flight coalescing,
canceled-lane bounds, engine shutdown, reserved live hedge, and refill shutdown.
A separate 30s-limit invocation after the fix covers the new rejected-swap
regression, copy safety across acquisition paths, stop cancellation and direct
fallback; it passed in 0.688s. Total reported package test time was 5.111s;
no benchmark time was spent.

## Remaining coordinated acceptance

Parent owns the final full gate and integration. Scryer/Weaver owner
`thr_6vmvajut38` confirmed zero edits to stream/fetch.go and usenet/fetcher.go:
UUdecode uses existing normalization; Redial already rebuilds pools. Its per-IP
observations must preserve decoded-meter semantics, demand bypass, admission,
and provider connection limits. No unfinished work was merged here.

Before a coordinated provider/Apple TV comparison, record the exact composed
head, provider/node limits, pipeline policy, concurrent viewer count, codecs and
bitrates, cache state, warmup boundary and measurement duration. Choose a total
offered media load supportable by independently measured usable capacity. Use
identical inputs/settings and a declared steady-state warmup for before/after.
Measure per-viewer delivered bytes over wall time, startup/seek latency, stalls,
buffer runway and adaptation outcomes; separately measure actual socket/wire
bytes and occupancy with agreed instrumentation. Include joins, seeks, pause,
cancellation and stop under contention; verify connection ceilings, bounded
speculation, original retention on incompatible candidates, and no viewer
starvation. Agree duration and success thresholds with the coordinated owner
before using results as a release gate. These measurements remain unperformed.

The earlier two-stream fixture demanded 20 MB/s on at most 12 MB/s raw capacity
before latency/encoding. Its 2.4–2.6 MB/s reader results have no like-for-like
baseline and prove neither regression nor improvement. No pool inflation or
raw-wire claim is warranted. Issue 385 remains open for real-flow acceptance.
