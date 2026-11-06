# Proposed authenticated encoder-host fencing

Execution safety uses [generation fencing and autonomous lease expiry](generation-fencing.md).
The host provider described here is optional physical-extinction evidence for
reclaiming uncertain old capacity; it is outside the playback recovery critical
path. Pod absence and Node NotReady never constitute this evidence. The existing
`hostfenceprobe` is read-only diagnostics, not an installed provider.

Design only. No provider, key, host process, daemon change, kill or automatic
claim-release path is installed by this document. CPU media and clean packet
timing must pass before any loss experiment. Windows JobObject fencing is a
separate later provider with separate verification rules.

## Outcome and present boundary

A future trusted host provider can prove that the exact old encoder execution
and its local input processes cannot execute again. That proof must bind the
configured cluster, complete placement fence, reservation operation and immutable
Start request hash to a previously observed Docker/CRI/kernel execution identity.
It cannot establish retirement of the remote source owner or its producers.

The current `hard-loss.sh` targets an immutable kind worker container ID and
observes Docker `running=false,pid=0`. That is useful infrastructure evidence,
but is **not** the independently authenticated process/cgroup proof proposed
here. The current runtime retirement verifier does not consume host attestations;
its evidence string is not an authorization mechanism. No script may write
`retired=1`, delete claims or pass a fabricated always-true callback to bridge
that missing integration.

At design time, read-only Docker inventory reported:

- daemon ID `a421ddf5-a7a2-4686-b318-6a51890566bb`, cgroup v2, systemd driver;
- kind worker2 ID
  `3d7cb3ab6ce05d80fcfa5ee8f6c53987ede247ec5d536a3c820034b487e431ca`,
  label cluster `ether035-ha`, role `worker`;
- container start `2026-11-05T16:28:21.218723485Z`, daemon-host PID `1829647`.

These observations do not authorize a kill and do not supply a retained pidfd,
PID namespace identity or host cgroup handle. PID numbers returned by Docker
must not be interpreted using an unrelated sandbox's `/proc` mount. Before a
future experiment, recapture the entire binding from a verified host vantage;
any object mismatch aborts rather than following the reused name.

## Trust and deployment

The lab provider must run outside the target kind node, on its actual Docker
host with the host PID namespace and real cgroup-v2 hierarchy visible. Its host
kernel, runtime and administrator are trusted. It uses the explicitly selected
local Docker Unix socket, not an application-supplied `DOCKER_HOST`, and verifies
the configured daemon identity. A remote provider requires independently pinned
server authentication and a narrowly authorized administrative client. Kubernetes
Pod/Node status and workload ServiceAccount credentials do not confer host-fence
authority.

The provider has a separate root-owned Ed25519 signing key. A deployment pins its
public key, provider identity, allowed host/runtime identity and execution scope
as trusted constructor configuration. Neither the key nor a writable trust store
belongs in Ether pods, workload Secrets, PostgreSQL settings, Helm values or the
shared cluster authority. Current replicas possess the deployment issuer key;
that issuer therefore cannot independently authenticate their host's death.
The signing key proves who made an observation, not whether the observation is
true; reviewed host code and kernel containment are part of the trust boundary.

Production needs an explicitly implemented and provisioned provider: for example,
a host supervisor that owns a non-escapable execution cgroup and restart admission,
or a separately authenticated infrastructure fencing authority with equivalent
whole-host termination guarantees. A privileged kind container is an isolated
lab topology, not production proof against a compromised host kernel or a
workload allowed to escape/migrate out of containment. Without the provisioned
provider, host fencing is unavailable and uncertain capacity stays charged.
No host agent, broad DaemonSet, Docker socket mount into Ether, anonymous network
mutation endpoint or host service is introduced by this design.

## Before failure: bind and arm

1. Read the exact current durable placement and source route. Capture configured
   cluster authority, session, generation, controller ID/boot, encoder ID/boot,
   reservation operation, exact accepted Start hash, source ID/boot, PlayID,
   source revision/size and claimed scopes. Session name alone is insufficient.
   The recovery operation supplies a cryptographically random challenge nonce.
2. Independently read the Kubernetes API using an authenticated lab identity:
   namespace, Pod UID, Kubernetes Node UID/name, container name, exact CRI
   container ID and runtime imageID. Resolve that CRI task through the nested
   runtime to its actual host task. Confirm its cgroup and PID-namespace ancestry
   descend from the selected kind node's containment. Bind the application node
   and boot using the authenticated runtime identity plus the durable assignment,
   not merely a label supplied by the workload.
3. Capture Docker daemon identity, host kernel boot UUID, exact outer container
   ID/start timestamp and labels in one response. Open a pidfd for the actual
   namespace-init process, retaining host start ticks and verifying `NSpid` ends
   in 1. Retain namespace descriptor/inode and its ancestor relationship to the
   encoder task. Bracket opening handles with identity rechecks; PID/name reuse
   or a changed container start aborts. Never signal or later re-open by bare PID.
4. Resolve the exact enclosing cgroup from the verified host task. Retain a
   directory handle, mount identity and inode, and open `cgroup.events` before
   action. Verify both encoder and input/FFmpeg descendants lie in that subtree.
   The provider must own the containment/restart boundary: no untrusted migration
   out, task insertion from elsewhere, namespace escape or launch outside it.
   Merely listing a process tree once cannot establish this property.
5. Persist an authenticated armed binding outside the victim before any kill.
   It commits all identities above and a provider operation ID. Enumerate every
   other workload/assignment sharing the kind-node kill scope; the eventual
   action needs explicit authorization for that whole scope. Keep source,
   controller, delivery and placement authority on surviving infrastructure.

If the encoder was not bound while alive, do not reconstruct its execution from
Pod absence after failure. A missing live handle is an unsupported/uncertain
result unless a separately trusted provider already recorded the exact binding.

## Authorized action and required death observations

The future provider operates only on its armed immutable container ID and retained
kernel handles. It first durably seals admission for the old execution instance
and old boot: no restart, restore, checkpoint resume or stale Start may revive
that instance. This sealing must be enforced by the actual launch authority and
runtime boot/fence checks, not an in-memory boolean in a test collector. A later
normal node start obtains fresh kernel instance/namespace and a new application
boot. Proof publication and consumption must not lift the old-instance seal.

The provider may then perform the separately authorized exact-object stop/kill.
Docker stop returning is not success evidence. Require all of:

- the retained namespace-init pidfd signals termination, and retained encoder/input
  task handles signal termination where captured;
- the same retained outer cgroup reports `populated=0`, which covers live processes
  in its complete descendant hierarchy, including nested pod namespaces;
- independent runtime inspection of the same container instance reports stopped
  and no active init/task, with no intervening restart/start-identity change;
- the admission seal remains durable and enforced through publication/consumption.

A pidfd avoids confusing a reused PID with the captured process. Linux kills the
members of a PID namespace when its init exits and prevents creating new processes
in that dead namespace. That kernel guarantee complements the hierarchy-empty
observation; the plan does not infer descendant death merely from parent-process
absence. Source: [Linux PID namespaces](https://man7.org/linux/man-pages/man7/pid_namespaces.7.html)
and [pidfd lifecycle observation](https://man7.org/linux/man-pages/man2/pidfd_open.2.html).

The cgroup check uses an already opened real host hierarchy handle, not a
recreated path or the pod's namespaced view. `populated=0` is hierarchical.
A future explicitly authorized `cgroup.kill` operation can cover concurrent forks,
but still requires observed completion and the maintained admission boundary.
See [kernel cgroup-v2 interfaces](https://docs.kernel.org/admin-guide/cgroup-v2.html).
If Docker removes the cgroup before the observer captures authenticated empty
state, an absent file/ENOENT is **not** substituted for that observation. A reliable
production supervisor should retain its own enclosing cgroup until attestation.
The existing lab runtime may lack that guarantee: fail closed and report the
measurement/provider limitation instead of weakening the predicate. Frozen,
unreachable, terminating, unready and expired are all nonterminal for this proof.

## Signed evidence and consumption

The proposed versioned record includes provider/key ID, purpose
`ether.encoder-host-fence.v1`, challenge nonce, provider operation/sequence,
configured cluster, full exact fence, reservation operation/Start hash, source
binding, imageID, all Docker/CRI/Pod/Node/kernel/cgroup identities, the armed-binding
hash, monotonic observation ordering, termination observations and admission-seal
identity. Timestamps are audit fields, never the death predicate. It contains no
bearer grants, DB URLs, environment dump, process argv or provider credentials.
Sign a bounded deterministic fixed-order binary encoding using length-prefixed
strings and integer fields; the transport envelope carries bytes, key ID and
signature. Reject unknown versions, oversize fields and ambiguous encodings.

The provider retains the full immutable signed record in its authenticated proof
store; a durable history receipt may contain its digest. A digest alone or a
caller-selected URL is insufficient. The trusted verifier authenticates the
signature and provider scope, retrieves the pre-armed binding, checks the exact
nonce/operation/fence/boot/Start hash and proves the enforced seal remains valid.
Replay of the same immutable evidence may be idempotent for its old exact fence;
it must never apply to a replacement boot or higher generation.

App/runtime owners must add an explicit optional trusted provider dependency.
A validated host proof can satisfy **encoder execution and local-input extinction
only** when its containment covered both. Source retirement remains a separate
live authenticated owner operation: exact source/PlayID/revision, backend Done,
nil retirement error and joined producers. A closed TCP connection, zero holds,
source lease expiry or dead encoder host is not source retirement. If the source
host was also lost, it needs its own approved proof/provider and lifecycle rules.

Only after the complete encoder/input/source proof chain passes may the existing
placement authority recheck the complete current fence under its transaction lock
and atomically record retirement/receipt and release claims. Mismatch, unknown
result, provider crash, missing proof or partition leaves claims held. This is a
new real verifier integration requiring owner implementation/review, not a chart
hook and not a replacement for current exact-runtime retirement.

## Review and acceptance gates

Before enabling the provider, exercise actual signed positive evidence and
negative cases: wrong daemon/host boot/container start/Pod UID/CRI task/application
boot/fence/Start hash/key/nonce; container-name/PID/cgroup-path reuse; surviving
FFmpeg grandchild or concurrent fork; denied migration/escape; cgroup removed
before empty observation; restart racing capture/action/publication; provider
crash/timeout/partition; tampered receipt and stale-generation replay. Failure
must preserve the exact uncertain claims. Check that unrelated containers and
source authority remain untouched, and that return-to-service uses a fresh boot.

Then pair a real authorized node-loss event with uninterrupted measured media,
exact source/controller authority and complete retirement/replacement evidence.
The present CPU HLS request failed before allocation; none of those loss gates
has run. Native Windows JobObject evidence must use retained exact process/job
handles and its own authenticated provider, not claim the Linux proof applies.
