# Encoder-node recovery

The controller probes the exact accepted worker with status RPCs from `Tick`.
`Options.EncoderRPCDeadline` defaults to 500 ms and
`Options.EncoderFailureGrace` to 2 seconds. Membership rejection, expired worker
leases and failed RPCs feed the same detector. Successful status reporting a
pending encoder retirement means the encoder is still active. The private
runtime calls Tick once per second, with a three-second aggregate control budget.
For a scheduled session with an available control/database path, detection is
bounded by the grace plus two tick intervals plus one RPC deadline (4.5 seconds
at defaults). Fleet saturation or database unavailability can delay scheduling;
this is not a universal wall-clock recovery guarantee.

Replacement selection excludes the failed node and keeps the current source
owner, revision, release and prepared PlayID. A source-generation counter resets
for the new encoder generation. The original admitted profile, quality decision
and audio-copy plan are retained. The supported recovery path is fixed-grid,
single-rung MPEG-TS HLS with reencoded video and copied audio. Remux, subtitle,
DASH, HTTP, live and prior byte-cursor recovery requests retain their existing
contracts and do not acquire this recovery path.

A placement supersession transaction moves the old claims into separately
charged displaced history, reserves the replacement, seals the old durable
operation gate, records the exact next segment/immutable request and recovery
event, and advances the route generation. No missing member, RPC error or elapsed
lease is recorded as process retirement. Displaced claims remain charged until
an authenticated exact worker stop/status reports encoder AND input retirement.
A fresh authenticated membership observation of the same node ID with a different
boot also retires displaced claims. This reconciliation reads durable history and
survives loss of controller-local old-session pointers. A missing member or stale
replacement policy row is insufficient. For a permanently removed host, an
operator can attest its exact boot has been removed/fenced:

```sh
ether cluster retire-node --node-id NODE --boot BOOT --evidence INCIDENT-REFERENCE
```

This command requires enrolled PostgreSQL credentials and verifies cluster
identity. It records an exact-boot removal tombstone, removes only that boot's
placement policy and releases only its displaced claims in one transaction.
The removed boot cannot be re-admitted; a new incarnation is independent.
This is an operator attestation, never automatic liveness pruning. Displaced history is bounded by placement's session
limit. Resource capacity checks include both current and displaced claims.
Unknown outcomes retry the identical retained replacement operation.

Every Start is checked against the exact unsealed durable admission/hash.
Before backend startup, the host authenticates the worker's actual
AcceptedStartHandle and records its immutable acceptance and arm association.
The new execution uses `Resume{FirstSegment: N, Generation: G+1}`. N is one past
delivery's fully retained contiguous publication floor, not the worker's encoder
progress or a guessed timestamp. Encoder argument/grid and AAC continuity are
owned by the splice implementation; this controller does not infer AU continuity.

A stable delivery wrapper keeps the public session identity. Retained segments
below N keep their original generation and bytes. Requests at or above N wait
for the replacement, bounded by `Options.EncoderSegmentWait` (30 seconds by
default). Segment attempts have two-second deadlines. Responses are buffered and
checked against the current fence before any client bytes are written, so a late
old-generation response cannot publish. Four concurrent response buffers of at
most 16 MiB are admitted per runtime; the surviving-controller retention store
has a 256 MiB global / 64 MiB per-session bound. These are lazy allocations.
Retention never evicts committed bytes. Exhaustion prevents further durable
publication; it does not justify advancing the restart watermark.

The 30-second request wait bounds a failed client request, not a successful
interruption guarantee. Detection, placement, source binding and encoder startup
must all fit inside the actual player's forward buffer. A database outage, no
eligible spare, or startup exceeding that buffer remains an acceptance failure.
Controller-local retained bytes are volatile; independent controller loss and
remote-session adoption have their own ownership implementation.

`RecoveryEvents(ctx, limit)` returns at most 256 durable events per cluster,
newest first. Recovered events report controller-observed milliseconds from first
failure observation to replacement Start acknowledgement. They omit client stall
counts because no player measurement occurred. Events and exact recovery decisions
use clusterplayback migration 7; displaced accounting uses placement migration 4.

`TestEncoderLossRecoveryInvariant` uses two fake encoders, a fake source, real
mTLS RPC and SQLite. It covers encoder exit and an alive but unreachable worker,
generation increment, immutable plan/resume cursor, sealed/armed old gate,
late-output rejection, complete unique segments, exact displaced capacity release,
final encoder/input retirement and output disposal. Additional cases cover an
unreachable old boot replaced by a freshly authenticated boot, controller-local
old-session history loss, and explicit permanent-node removal/tombstone fencing. The TS bytes are an offline
fixture. It proves the ownership/control invariant, not real video decoding,
PTS/audio continuity, PostgreSQL runtime behavior or zero client stalls. Those
require the release coordinator's real playback failure matrix.
