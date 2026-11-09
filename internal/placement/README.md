# Durable placement primitive

This opt-in package has no app wiring and changes no standalone, Plex, or local
transcode behavior. It requires a database opened by `internal/store`, which
registers/translates its component-prefixed migration for SQLite and PostgreSQL
14. Constructors explicitly receive the database, options, and retirement
verifier. There is no process singleton or background worker.

Trusted control-plane integration:

1. Create a service with a positive ledger quota, maximum lease (1 second to
   1 hour), and transaction/lock timeout (positive, at most 1 minute). Use
   consistent options across coordinators.
2. `SetPool(scope, role, capacity)` persists a resource budget. Zero denies
   admission; absent scopes never imply unlimited capacity. Share a scope across
   processes using the same physical GPU or other resource. Workers must also
   enforce their own physical limits. This is admission accounting, not bandwidth
   shaping.
3. An authenticated membership/policy reconciler calls `SetTarget(target, roles,
   requiredScopesByRole)`. Empty roles disable admission. Each enabled role needs
   1–16 explicit, distinct scope names. Startup must not blindly replace a live
   membership incarnation. Policy updates never clear existing claims, including
   claims from an old boot. Policy changes govern new assignments, not automatic
   revocation of existing work. Capability freshness, drain, and protocol policy
   are the reconciler's responsibility; this package is not a policy evaluator.
4. `Reserve` supplies stable session/controller identities, controller boot,
   target node/boot, role, operation key, expected generation, required claims,
   and bounded lease. All configured scopes are mandatory. Persist the returned
   full fence in transport DTOs, worker execution state, and output ownership.
   Generation zero means a previously unused session. Reassignment increments
   the durable generation only after retirement proof freed the previous claims.
5. Retry an uncertain reservation with the same operation and immutable payload.
   Canonicalized scope ordering does not change identity. Changing any other
   request field under the current key returns `ErrConflict`. A replay never
   reserves again or inherits later lease extensions. It returns the initial
   assignment/deadline and fresh authority/retired state; retired replay cannot
   start work. Old-generation requests remain fenced after replacement.
6. `Renew(fence, absoluteDeadline)` monotonically extends live authority within
   the maximum lease. The absolute deadline makes repeated calls idempotent;
   expired authority cannot be revived. `Lookup` returns the current deadline,
   immutable initial deadline, and fresh DB-clock authority state. Claims in a
   Record describe the original allocation even after retirement; accounting
   rows, rather than this historical field, determine capacity use.
7. Timeouts and database errors are `UnknownOutcome`, including ambiguous commits.
   Reconcile via `Lookup` and same-operation retry. A deadline is authority loss,
   not encoder retirement. Status/stop/retirement transport credentials must stay
   usable after expiry while start/media authority is denied. No time-based
   reclaim, unreachable-node reclaim, or startup cleanup exists.
8. `Retire(fence, evidence)` invokes the injected verifier **outside** transactions
   and then checks the full fence under the transaction lock before release.
   Missing verifier fails closed. Verification must establish authenticated
   exact-generation process retirement, or trusted operator attestation of
   external host fencing. Permanent node loss can therefore strand capacity until
   an operator proves fencing. Network silence and elapsed time are insufficient.
   A verifier is a trust boundary, not an implementation provided here.

All mutations (and consistent Lookup) use one component-wide transaction lock;
PostgreSQL uses the existing advisory-lock facility, and SQLite obtains a real
write lock before reads, including with deferred in-memory transactions. The
context/timeout bounds lock contention. This correctness-first serialization
limits throughput; there is no speculative lock sharding. Queries touch only the
bounded placement ledger and indexed scope accounting, not the media library.

Retirement deletes accounting rows but retains one bounded tombstone per logical
session, with generation floor, bounded identities, operation hash, retirement
time and at most 16 historical claims. The quota (`MaxSessions`) bounds **live**
(unretired) generations only, counted through a partial index bounded by pool
capacity; retained tombstones never refuse admission. `LedgerUsage` reports live
plus a maintained retained-tombstone counter without scanning history. Quota
exhaustion is `ErrQuota`.

`PruneTombstones` (run by exactly one node, off request paths) compacts terminal
tombstones in bounded batches under the ledger lock: retired, no displaced
generation still charged, and both the authority deadline and the retirement at
least `Age` (≥ `MinRetention`, ≥ `MaxLease`) old on the database clock. Each
batch removes the session's history receipts, lets the caller delete its own
per-session rows in the same transaction, and raises one cluster-wide generation
floor. A session with no row is always allocated a generation above that floor,
so any fence that survives anywhere for a compacted session is strictly older
than every generation the ledger can issue again; a delayed replacement for an
old generation still fails `ErrFenced`. `ReconcileRetained` corrects the display
counter. Never delete tombstones by hand: only the floor makes compaction safe.

The transport and workers must enforce these fences and execution deadlines;
this package alone does not stop FFmpeg, validate retirement evidence, provide
clock synchronization, or authorize peers. Database clock sets authority.
Remote execution needs its own conservative clock-skew/expiry enforcement rule.
No leadership epoch is used as an execution generation.

Focused gate: `GOMAXPROCS=2 CGO_ENABLED=0 go test -p 1 ./internal/placement`.
`store.OpenTest` automatically uses isolated PostgreSQL schemas when
`ETHER_TEST_POSTGRES` is set. PostgreSQL 14 is included in the existing CI matrix.
