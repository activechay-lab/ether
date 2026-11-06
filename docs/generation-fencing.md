# Execution generation fencing

The session execution generation is the authority token. Every encoder identity,
source scope, durable route, placement renewal and retirement completion carries
its complete placement fence (session, generation, controller and target boots).
Generation changes belong to the controller supersession transaction. This
consumer layer never manufactures retirement evidence or releases claims on
expiry. A stale completion is rechecked under the placement transaction lock
before any claim deletion, including historical receipt publication.

Worker RPC admission checks the durable route and live placement. Source Verify,
Acquire and Renew independently check the same fence. Runtime input gateways
validate that fence before local HTTP headers and each body write. Delivery
checks the selected route again and, for encoded sessions, its live exact
placement before forwarding headers or bytes. Preparation-only direct playback
uses a generation-zero route until encoder assignment and checks the exact route;
it confers no encoder authority. Retained output also checks current authority
before capture and publication, and its store uses generation-keyed entries/CAS.

The `generationfence.Writer` repeats these checks at publication, including when
a producer resumes after a blocked read. Old workers cannot finish a delayed
manifest or segment after observing supersession. A failed check before response
commit returns 409; after commit it refuses further body bytes. Bytes already
admitted to a socket write cannot be recalled. This is admission ordering, not a
claim that the network retracts data already delivered. The checks add authority
lookups per write; throughput under the final workload still needs measurement.
Public Plex URL and successful response formatting are unchanged.

## Independent termination and leak bounds

The worker arms its existing local monotonic timer at accepted Start and after
an accepted renewal. Failed renewals and duplicate replays never extend the cap.
At the earlier of wall expiry and that monotonic deadline it cancels startup,
seals input and retires its owned encoder. Each actual HLS/HTTP encoder process
also watches its lifetime cancellation independently of session locks and the
shared cleanup queue. Cancellation closes the owned guardian pipe; the guardian
kills the FFmpeg process group (or its platform containment). A late process
returning from Start immediately observes the same cancellation.

For a responsive local scheduler/kernel, termination is requested by the last
accepted execution deadline: the runtime initially grants two minutes minus
clock skew, and worker admission permits at most ten minutes per accepted lease.
This bounds continued execution after the last accepted renewal; it does not
promise a finite syscall/scheduling delay on a frozen kernel. Actual process exit
and producer drainage remain required for retirement. Uncertain or stuck cleanup
retains accounting rather than declaring resources free.

Source bindings and each bearer have monotonic local caps, conservatively reduced
by configured clock skew. A source binding without a first grant expires within
MaxLifetime (runtime ten minutes); successful issuance/renewal replaces its timer
with the granted deadline. Expiry permanently seals the acquisition barrier and
removes its credentials without consulting the controller. Timer callbacks compare
the accepted cap, so an old callback cannot revoke a new renewal. Existing byte
requests have independently scheduled context deadlines and recheck authority at
publication. Already acquired producers retain ownership until they actually exit.

The local input gateway separately arms the accepted Start deadline and updates
it only after successful source renewal. Expiry seals its tracker, cancels reads
and closes its source reader. Worker rejection after source extension still seals
that input on the worker's own shorter bound. None of these timers releases a
replacement's durable capacity or evicts responsibility tombstones.

## Kubernetes and host observations

Safety requires neither Kubernetes API access nor host death attestation. Pod
absence or Node NotReady may trigger an earlier controller recovery attempt;
they cannot authorize `Retire` or assert that a process died. This layer adds no
Kubernetes watcher, permission, privileged endpoint or enabled accelerator.
`internal/hostfenceprobe` remains optional read-only diagnostics. The authenticated
provider in `kubernetes-hard-fencing.md` remains a possible way to prove exact
physical extinction and reclaim uncertain old capacity sooner. It is outside the
playback authority critical path and is not implemented by these diagnostics.

## Offline evidence boundary

`TestPartitionedGenerationCannotPublishRenewOrReleaseReplacement` leaves an
encoder double running while advancing durable generation. It checks delayed
manifest rejection, segment rejection, worker/source renewals, source grants,
local gateway and delivery writes, stale placement renewal/completion, autonomous
worker expiry and replacement claim retention. Its SQL generation change models
a committed controller transition; it does not test the controller's supersession
implementation. `TestEncoderLifetimeFencesProcessOutsideCleanupQueue` exercises
the actual guardian with the existing helper executable and a held lifecycle lock,
including late startup. Neither test is real FFmpeg or Kubernetes playback-loss
acceptance; that remains the release lab's measured integration gate.
