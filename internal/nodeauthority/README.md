# Boot-local source authority

Unwired host adapter only. No RPC issuance endpoint, scheduler, listener, SQL
credentials, runtime claim, source capture, provider URL or caller path. Construct
one Authority per actual source-owner process boot. Owner is that node's existing
membership identity/incarnation. A restarted process constructs a fresh empty
registry, invalidating every old local binding and bearer; this says nothing
about old remote encoders retiring or durable placement capacity being free.

New requires a fresh placement Lookup dependency (`*placement.Service` satisfies),
current Policy, source-owner identity, clock and explicit bounded capacities.
Policy receives Owner and the complete immutable Scope. It must check live viewer
and source rights, controller membership/boot/current authority, local source-owner
membership/boot and target membership/boot/transcode eligibility including drains
or role changes. Placement Record.Role independently must be transcode. Every
issuance, renewal, Verify and Acquire queries Lookup anew, rejecting lookup errors,
retirement, Authority=false and any difference in the full durable fence. No cached
DB authority or prior successful policy decision substitutes for a current check.

Trusted host code captures an existing playback SourceHandle using SourceAccess,
then Bind(scope, handle, exactSize). Canonical handle Revision and Size must match;
no network data enters Capture here. Bind checks Active and policy/placement.
Handle.Value() is the stable immutable Start.Grant, never the bearer. Trusted host
composition uses Resolve(ctx, authenticatedIntendedPeer, stableValue) to recover
the same Handle, Scope and size after fresh Active/policy/DB/epoch checks; wrong
peers, stale fences and sealed bindings fail closed. Resolve installs no endpoint
and issues no credential. Issue produces
one Lease. Lease.Grant() explicitly exports the nodesource transport credential;
that existing DTO contains a raw string and MUST NOT be logged or formatted.
Authority, Lease and Handle redact ordinary and Go formatting; errors suppress
upstream details. Credentials and stable handles use independent crypto/rand
256-bit values. Never use a stable handle to resolve another source or scope.

Install Authority as nodesource.Options.Authority and Authority.Acquire as its
Acquire callback only on the authenticated private handler. Verify binds the
bearer to the exact intended target NodeID and Boot. It checks Active and live
policy/fence at every HEAD/GET and at nodesource's reader revalidation cadence.
Acquire independently checks current authority and exact binding, then invokes
SourceHandle.Acquire under playback's atomic manager barrier. The handler must
install its ReadRetirement tracker first and preserve Release even on errors.
Acquire is a trusted adapter, not a bearerless public endpoint.

Revoke first seals the binding epoch and deletes all its credentials under the
registry mutex. Outside that mutex it calls SourceHandle.Revoke, closing playback's
acquisition barrier before returning. Calls in flight may have retained a reference
before that barrier; their epoch recheck returns an error with Release preserved
and no usable source facade. An acquisition that already completed its epoch check
before sealing is an existing hold. Once Revoke returns, no late call can acquire
a new generation reference. Repeated Revoke also crosses that barrier. All external
policy, DB, clock, handle and metadata calls run outside registry locks. No callback
can unseal or rebind an entry. Epoch changes once, so cannot wrap. Playback itself
rejects replaced, sealed, detached and revoked handles at its own manager barrier.

Renew requires the current exact grant, unchanged Scope/handle/size and strictly
later expiry. It rotates the bearer without mutating any existing grant. Prior
bearers keep only their ORIGINAL expiry; a replay of a successful renewal cannot
mint again. Concurrent renewals commit at most once. MaxGrantsPerBinding includes
current and unexpired old grants, and MaxGrants caps all retained credentials.
Expired credential eviction on mint only frees credential slots: no hold release,
backend Close, binding release or placement.Retire occurs. MaxBindings bounds live
bindings plus sealed ones not yet forgotten. Forget(ctx, grace, limit) drops a
binding only when it is sealed, its acquisition barrier closed, it stayed sealed
for grace, and a fresh Lookup shows its exact fence retired, superseded or
compacted, so placement can never re-authorise that scope and it still cannot be
rebound. Revoke of a forgotten handle is a no-op. Exhaustion of live bindings fails
closed. Maps and scans never exceed explicit caps (4096).
No timers, goroutines, unbounded operation history or per-producer work are added.

ClockBoundKnown is a trusted deployment assumption, not a measured guarantee:
local source, durable DB and worker clocks must have pairwise skew <= ClockSkew,
including drift throughout MaxLifetime. Unknown skew cannot mint. ClockSkew must
be finite, nonnegative, at most a minute and below MaxLifetime (at most an hour).
Expiry must be strictly after local now+skew, and at most local now+lifetime-skew
and the freshly observed record deadline-skew. Old grants stop at their immutable
expiry; Verify conservatively rejects within ClockSkew of raw Grant.Expires.
Worker source-evidence composition must use this earlier effective deadline: raw
Expires cannot promise availability through a worker deadline. All authority remains
revocable at any time. Clock discontinuities beyond that bound
violate the deployment assumption. All authority observations are point-in-time;
external policy and DB transitions cannot be made atomic by this local registry.

Host owns nodesource.Handler.Reconcile and SourceAccess.Reconcile during activity,
drain and after expiry with no traffic. The latter can BLOCK in backend Close and
belongs on an explicit cleanup executor. Registry expiry/revocation never calls it.
Release is accounting only. Actual SourceRetirement.Done/Err and independent encoder
retirement proof govern cleanup and durable retirement. Request completion, expired
credentials, canceled reads and zero pending local holds are never final source or
remote encoder proof. Source lifetime release belongs only to the trusted owner
flow. There is no automatic placement.Retire, even after permanent node loss.
