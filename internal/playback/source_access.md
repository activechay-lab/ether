# Exact source access for trusted host composition

`SourceAccess` is an unwired local capability registry, with no network imports,
route, credential authority or policy bypass. Create one per Manager with explicit
handle/hold caps. Admission, including Capture, is constant work independent of
library size; Reconcile scans only the bounded hold registry. Handles are opaque
Go objects, never recovered from wire strings. Revoke handles when local bindings
are retired; there is no eviction timer. Stale handles intentionally fail closed
for provisional, historical, replaced, detached or sealed generations, even if
another session reuses the public session ID or canonical revision.

Capture requires the exact previously observed `SourceRetirement`, owning numeric
viewer ID, manager-issued input URL and canonical source revision. The URL is
only a trusted local equality binding; never accept it from a request. Capture
compares revision to the generation's historical open observation, and freezes
its actual published byte size. Acquire retains under manager → session →
lifetime → access → generation ordering; replacement and detach/seal cannot pass
between that check and retention. Fresh canonical revision/size validation occurs
outside locks, after registering the hold, and errors can therefore return an
owned hold. No source metadata callback, fetch or Close runs under those locks.

The read facade delegates only to the captured source, pins size, disables Close,
and requires ContextReaderAt support; it creates no goroutines to adapt legacy
ReaderAt. It registers synchronous reads and passes the acquisition tracker to
sources even when a caller supplies a different context. Source implementations
must register delayed producers before launching them, including on read errors.
The request owner must install ReadRetirement before Acquire. Release seals that
original tracker, rejects new reads, and does only bounded accounting. Acquired
requests keep their object until all registered producers drain and reconciliation
actually drops the existing generation reference. Releases are idempotent.

**SourceAccess.Reconcile can block in backend Close.** Run it only on an explicit
cleanup executor, never on heartbeat, lease expiry, request or bounded callback
paths. It owns no goroutines. Concurrent reconciliation retains capacity through
Close and never drops the same reference twice. Call it after transport releases,
during drain, and after expiry even if HTTP traffic has ceased. Pending counts or
zero returned by Reconcile prove only local hold accounting, not successful Close
or durable cleanup: the original SourceRetirement.Done/Err remain authoritative.
EncoderInputLease/InputDone and remote request completion are separate evidence.

Revisions use core.SourceRevision change detection, not content hashes. Unknown
revisions fail closed; arbitrary caller revision strings cannot bless new bytes.
The source/resolver must guarantee immutable bytes for that revision. Metadata
checks cannot prove immutability against concurrent in-place file modification.
No durable grants, placement authority, provider budgets, preparation, failover,
stream selection or standalone frontend behavior are changed here.

A narrow host adapter can look like this (in the composition package, not playback):

```go
// At trusted local preparation: retain the exact outcome and issued input URL.
// canonicalRevision comes from the prepared source observation, never the wire.
access, err := playback.NewSourceAccess(manager, 128, 256)
// ... handle err ...
handle, err := access.Capture(ctx, user.ID, session.ID, outcome,
    capturedInputURL, canonicalRevision)
// ... handle err ...
// Store scope -> handle in the host's bounded, synchronized authority registry.
// Bind the complete fence, viewer and revision, including both node boots.

acquire := func(ctx context.Context, scope nodesource.Scope) (nodesource.Hold, error) {
    // Verify current full placement and current viewer policy on EVERY request.
    // Authority.Verify already enforces these; recheck atomically with the host
    // binding lookup if host placement/binding retirement requires that barrier.
    binding, err := authority.ExactLocalBinding(ctx, scope)
    if err != nil { return nodesource.Hold{}, err }
    // Binding contains the opaque local handle and its original scope.
    if binding.Scope != scope || binding.Handle.Revision() != scope.Revision {
        return nodesource.Hold{}, core.ErrNotAvailable
    }
    held, err := binding.Handle.Acquire(ctx) // handler installed ReadRetirement
    // Preserve a nonempty hold on error, so handler retains responsibility.
    return nodesource.Hold{Scope: scope, Source: held.Source, Release: held.Release}, err
}
// Install acquire only when composing the private authenticated transport.
// nodesource.Reconcile calls bounded Release only after its request tracker drains.
// Then lifecycle's explicit cleanup executor calls access.Reconcile (may block).
// Retiring host binding: remove binding, handle.Revoke(), retire exact lifetime;
// outstanding holds must still be reconciled. Consult outcome.Done()/Err().
```

`ExactLocalBinding` above is host-owned pseudocode, not a supplied authority. The
host must bound bindings, verify the entire placement fence and viewer policy on
every request/revalidation, and coordinate revocation with its own authority.
Playback only enforces its exact local lifetime/generation/viewer ownership gate.
