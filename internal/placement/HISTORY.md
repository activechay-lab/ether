# Durable exact retirement receipts

This is an additive, unwired foundation. `NewHistory(placementService,
configuredCluster, receiptVerifier)` explicitly shares the placement DB and lock
policy. The configured namespace comes from trusted host configuration. The
verifier must authenticate that namespace against the actual cluster authority;
it must not accept a namespace asserted by an RPC. First successful publication
pins the ledger namespace durably; other configured namespaces fail closed.
PostgreSQL is the fleet authority; `store.OpenTest` exercises the same contract on
SQLite or an explicitly supplied PostgreSQL test database.

`RetireWithReceipt` is the preferred publication path. For a new receipt it runs
both existing `RetirementVerifier` and `ReceiptVerifier` outside registry/DB locks,
then rechecks the full placement fence and reservation operation under the
existing `placement:all` transaction lock. Namespace policy, immutable receipt
insertion, normal retirement and claim release commit together. Reentrant proof
callbacks cannot release a replacement's claims. Target boot changes, expiry and
quota policy disagreement do not themselves prove retirement or prevent trusted
retirement of an old exact assignment; standalone `Retire` semantics are unchanged.

`Record` only publishes an already retired, still-current exact assignment. It
never releases claims. If a replacement wins before publication it fails closed;
retained payloads cannot be discarded. Already stored immutable replay succeeds
after replacement without needing the discarded proof objects again. Changed
payloads conflict. `Lookup` uses the full indexed namespace/fence key and returns
trusted exact historical evidence or unknown. Historical evidence is never current
authority. Every SQL result/commit failure remains an `UnknownOutcome`; reconcile
with exact `Lookup` and compare the immutable receipt before releasing ownership.

Receipts contain bounded identifiers, source owner boot + PlayID + revision + size,
immutable request hash, separate authenticated encoder/input/source proof digests,
and optional worker-accepted renewal CAS summary. They contain no raw grant or
source capability. Source proof must include actual backend retirement and joined
source producers. Input drain, zero source holds, timeout, status absence, or an
unaccepted source renewal never substitute. `NeverStarted` requires separately
verified irreversible exact stop-before-start admission sealing; it cannot infer
absence. Its request hash/initial lease/renewal summary must be empty. Proof digests
are commitments to authenticated evidence; this package cannot authenticate them
without the injected trusted verifier.

## Consumer ownership and atomicity

No consumer compaction is implemented here. Consumer owners must serialize every
admission with compaction. One strategy is a process-lifetime registry epoch:

1. Under the admission mutex capture the epoch *before* fresh durable receipt and
   current placement lookup; do not acquire a backend or source responsibility yet.
2. Immediately before registering new responsibility under that same mutex,
   require the epoch unchanged and recheck local exact-object seals/tombstones.
   Any epoch change requires a fresh lookup. Fail closed on epoch overflow.
3. Compaction first seals the exact local object and increments the epoch under
   that mutex. Publish/lookup the receipt outside the mutex, then recheck exact
   object identity, fence, seal epoch and all terminal proofs under the mutex
   before removal. Increment the registry epoch on removal as well.
4. Join in-flight acquisition/issuance callbacks and preserve returned releases.
   Adapter responsibility transfer requires explicit exact-owner acknowledgement
   of the durable receipt before removing its Session/token. Unknown outcomes
   retain all responsibility. Acknowledgement does not grant replacement authority.

The essential race is a fresh durable check followed by a paused admission, a
concurrent tombstone deletion, then admission resuming against an empty registry.
A lookup immediately before locking is insufficient. All relevant entry points,
including Bind and negative stop-before-start, must participate in the same epoch
or equivalent serialized strategy. Other authority transitions retain their
existing fresh checks and barriers; the epoch is not a distributed execution lease.
Missing old status may return a matching trusted receipt or unknown, never a
manufactured retired result. A higher floor fences starts but is not exact proof.

There is no receipt/floor deletion API, TTL or LRU. Receipt disk usage grows per
retired generation; placement's permanent logical-session floors and explicit
fleet quota/CAS raises remain intact. This foundation does not remove the 4096
consumer lifetime-map limits. Random pre-reservation preparation/reopen operation
histories have no placement generation; retain them until a separate durable
operation-fence contract exists. SourceAccess's existing exact hold/handle recycler
is unchanged. No app wiring, runtime services, automatic retries or cleanup loops
are installed.
