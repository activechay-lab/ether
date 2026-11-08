# Ether on Kubernetes

This chart packages the v0.3.5 deployment runtime. Use an image that implements
`ETHER_DEPLOYMENT_FILE`, role isolation, authenticated private transport and the
probe/drain contract below. Older Ether images are incompatible. The integration
runtime composes server,
source, transcode and delivery specialists beneath the protocol adapters.
Mixed public-role deployment still requires the explicit chart lab gate described
below. Implemented recovery paths are not a seamless-playback certification:
that requires the measured loss matrix in [the acceptance guide](kubernetes-acceptance.md).

The chart uses external PostgreSQL, one StatefulSet for each enabled role pool,
and a separate persistent config volume for each pod ordinal. The default pool
is one full node. For availability, use at least two public full/server nodes,
replicated PostgreSQL managed independently, and enough source/transcode/delivery
capacity for the intended failure. Kubernetes and a singleton PostgreSQL pod do
not make the database highly available. Redis and a shared media filesystem are
not required. Media bytes never pass through PostgreSQL.

## Prepare the namespace and trusted inputs

Requirements: Kubernetes 1.32+, Helm 3.19+, a dynamic provisioner (or matching
preprovisioned PVCs), a working cluster DNS suffix, synchronized clocks with a
verified error bound (host NTP is mandatory), and an external PostgreSQL database
reachable from every pool. Select an explicit immutable image tag or digest before installation.

```sh
kubectl create namespace ether
# Prepare these files securely outside the repository. Do not print their bytes.
kubectl -n ether create secret generic ether-db --from-file=database-url=/secure/database-url
kubectl -n ether create secret generic ether-cluster --from-file=cluster-key=/secure/cluster-key
kubectl -n ether create secret generic ether-authority \
  --from-file=ca.crt=/secure/ca.crt --from-file=ca.key=/secure/ca.key \
  --from-file=roots.pem=/secure/roots.pem
```

The URL file contains the complete PostgreSQL URL, including authentication and
TLS settings. Prefer `sslmode=verify-full` with the database hostname and a CA
trusted by the image; use your database provider's CA installation mechanism if
its CA is private. Database TLS roots and Ether's private transport roots are
independent. The chart does not configure an external database or provision its
CA. Use a direct database connection or a session-mode pool: bootstrap/migrations
use PostgreSQL **session** advisory locks, so transaction-mode pooling is unsafe.
Budget database connections for all pool replicas and migrations.

`cluster-key` must be exactly 32 raw random bytes, not base64 text, a hex string,
or a PEM file. `ca.key` is an independent PKCS8 Ed25519 issuer private key;
`ca.crt` is its PEM CA certificate, with CA constraints and certificate-signing
usage. `roots.pem` explicitly contains trusted issuer certificates. The
cluster encryption key is not a TLS signing key. Each pod receives the trusted
issuer material needed for repeatable provisioning. Limit Secret access and
namespace administrative access accordingly; use your organization's external
Secret controller or encrypted GitOps integration for management.

Create these once for a new cluster using your trusted credential process.
Store backups independently of Helm and do not regenerate them during upgrades.
Secret values must never appear in Helm values, ConfigMaps, shell arguments,
CI artifacts, application logs or screenshots. `kubectl create secret` above
reads files; its normal output contains the resource name only. Do not add
`-o yaml` or shell tracing to credential setup commands.

## Install and scale

Review `packaging/kubernetes/mixed-cpu.values.yaml` and adjust the verified clock
bound, replica counts, resources and Secret names. The example asserts a 1000 ms
bound; replace that assertion with one you can actually establish. Default values
refuse to render until `runtime.clockBoundKnown=true` is supplied.

```sh
helm lint packaging/helm/ether -f packaging/kubernetes/mixed-cpu.values.yaml --strict
helm upgrade --install ether packaging/helm/ether -n ether \
  -f packaging/kubernetes/mixed-cpu.values.yaml --wait --timeout 15m
kubectl -n ether get pods,pvc,svc
```

`full` maps to the runtime’s empty roles array and runs all roles. `server`
provides browse/control and responses that combine admission with streaming;
`delivery` provides the fixed public representation sender. `source` and
`transcode` have no public Plex listeners. Enabling a pool in values does not
establish that its shipping graph passes playback acceptance.
The API Service selects only full/server pods; the separate media Service selects
only full/delivery pods. See the same-origin routing requirements below.
Specialists are runtime roles, not separate products; changing role mix does not
change Plex server identifiers, rows, personalization or policy.

Each pool has `replicas` and `bootstrap`. Only full/server pools may bootstrap.
With `bootstrap: true`, concurrent nodes seed-or-verify the shared database under
a session lock using the trusted Secret inputs. There is no per-replica one-use
join token. After creation, set bootstrap false to require existing settings.
A restart reuses the ordinal's own `node.json` and generates a fresh boot identity.
Never clone a config PVC or `node.json` into another ordinal or release. Adding
replicas creates new identities; removing replicas retains their PVCs. Reusing
that ordinal later reuses its identity, so first establish that the old process
and its work are retired. Pod disappearance, timeouts and silence do not prove
retirement of a source or encoder.

Use `helm upgrade ... --set pools.transcode.replicas=2` to scale out. Scale down
only after explicitly draining the affected highest ordinals and confirming
retirement. Helm's replica update itself does not invoke drain. Per-role PDBs
limit voluntary eviction; they do not protect direct pod deletion, forced loss,
or guarantee spare capacity. A one-replica pool with maxUnavailable 1 can become
fully unavailable. Choose PDB and scheduling rules to match the capacity plan.

## Continuous clock enforcement

`runtime.clockSkewMillis` is the maximum **pairwise** wall-clock error used by
lease and grant consumers; `runtime.clockBoundKnown=true` remains mandatory.
Every PostgreSQL membership heartbeat (normally five seconds) now performs one
additional bounded scalar `clock_timestamp()` query, before publishing caps.
The estimate is local wall time at the monotonic round-trip midpoint minus DB
time. Admission uses `abs(skew) + RTT/2 + 1 microsecond` (timestamp precision).
Each node must fit within **half** the configured pairwise bound against the
common database reference, so nodes on opposite sides of that reference cannot
exceed the peer-to-peer budget. A zero bound therefore cannot admit work.

Unknown, failed or stale samples (older than three heartbeat intervals) refuse
new authority. Local wall elapsed is also compared with monotonic elapsed on
heartbeats and authority checks. Paired observations are captured under the
same guard lock; delayed samples cannot rewind newer proof. Steps above the
smaller of 10 ms and one tenth of the bound (minimum 1 ms) are logged and carried
into the estimated offset. A correction within the existing half-bound budget
remains diagnostic; unknown or out-of-budget uncertainty closes authority.
Steps during a DB query are reported, with the after-wall estimate and measured
RTT determining admission. Two consecutive good samples at or below 80% of the
half-bound restore admission automatically.
Keep host NTP/time synchronization running; this monitor neither sets clocks nor
proves an external synchronization service's future drift guarantee. PostgreSQL
must remain a common stable clock reference for the cluster.

During a clock fault `/admissionz` returns HTTP 503 with `reason: "clock_skew"`.
Placement reservation, source binding/grant minting, worker start acceptance and
**renewal issuance and acceptance** fail closed. Extending a lease with an
unbounded clock could outlive a peer's fencing deadline, so existing leases
cannot be renewed as a special exception. Failed remote renewals are retried
without proactively revoking the last accepted lease; its original monotonic
expiry still bounds the worker and source authority. Clock quarantine itself
does not revoke grants, stop encoders, remove media routes or change `/readyz`.
Existing media remains subject to normal authority/expiry checks, and an outage
longer than the previously granted lifetime can therefore interrupt playback.
No retirement or resource reclamation follows from clock failure.

A healthy controller probes exact membership and encoder status before renewal.
Draining or clock quarantine governs new placement; it does not by itself
replace a healthy execution under an accepted lease. Refused renewal retains
the worker's last accepted monotonic deadline. Expiry self-fences execution;
a failed status or membership observation can then trigger recovery.
Supersession preserves
old displaced capacity claims until exact retirement proof, advances execution
generation, and resumes at the delivery watermark. A clock-quarantined controller
also stops coordinator lease issuance/renewal/adoption; a healthy replica can
win the next coordination epoch and continue recovery. This requires healthy
spare capacity and a surviving database/controller. The offline recovery fixture
covers quarantine with live RPC transport; it does not certify a client-buffer
budget or actual decoder/audio continuity. Those remain measured lab gates.

Heartbeat `caps.diagnostics.clock` and local cluster health expose
`clockSkewNs`, `rttNs`, `uncertaintyNs`, `boundNs`, `sampledAt`, `ready`, `reason`
and `lastStepNs`. The diagnostic readiness reason is `clock_skew` while blocked;
`readyExisting` remains the serving-readiness observation. Remote clock
diagnostics exclude nodes from new selection, while the node's own fresh guard
is the enforcement boundary. Diagnostics alone never grant authority.

## Durable ledger quota

`runtime.capacity` limits concurrent work at a node/resource scope (1–4096).
`runtime.ledgerCapacity` is the separate fleet quota on **live** (unretired)
placement sessions: default 1000000, range 4096–10000000. It is at least
concurrent capacity. Retired sessions never count against it, so retained
history can never close admission. Every replica must use the same ledger value.
This chart requires the runtime version that implements this field; earlier
staging candidates reject it as unknown.

Placement seeds or verifies a persisted fleet policy under its transaction lock.
Each new Reserve checks equality with that policy in the same transaction.
Changing Helm configuration alone never silently raises the quota. The cluster
leader compacts terminal sessions (retired, no displaced generation, deadline and
retirement both older than 24 hours) into one cluster-wide generation floor, so
the ledger stays proportional to live work plus a day of history, and a compacted
generation is never issued again. Admission status and transition logs expose
exhaustion. Existing work must remain serviceable when new allocations fail
closed.

To raise the quota, first use the trusted local administrative command on one
healthy pod (example raises 1000000 to 2000000):

```sh
kubectl -n ether exec ether-ether-full-0 -- /usr/lib/ether/ether cluster ledger-quota \
  --data-dir /config/ether --expect 1000000 --capacity 2000000
helm upgrade ether packaging/helm/ether -n ether -f your-values.yaml \
  --set runtime.ledgerCapacity=2000000
```

The command performs a monotonic compare-and-swap against the persisted policy.
A mismatch or attempted decrease fails. After raising, update the same fleet
value and use the sequential drain/replacement procedure below. During the
transition, stale coordinators keep existing work but reject new allocations
until their configured quota matches. Do not try to bypass the policy by editing
rows or deleting generation history by hand: only the leader's compaction raises
the generation floor that makes deletion safe. Plan this admission interruption before
raising the quota.

## Storage, probes and transport

Every ordinal gets a retained `data` PVC mounted at `/config`. New installs
create `/config/ether` as UID999 with mode0700 and use it as `ETHER_DATA_DIR`.
This protects identity/config files even when a provisioner creates the volume
root with broad permissions. The startup script fails closed if it finds a
legacy root `node.json`, `config.json` or `.credentials-key`; it never silently
creates a replacement identity under an existing root layout. Migrating that
layout requires a stopped, positively fenced node, a backup of its full data
directory, and an explicit move preserving its exact identity/key/config into
`/config/ether` with UID/GID999 ownership and mode0700. Do not migrate by copying
only node.json or generating a new identity. The rollout helper reads the exact
pod's advertised data directory and supports both layouts, so it drains existing
root-layout nodes using `/config` before a deliberate migration. Cache defaults to
an independent disk-backed emptyDir at `/cache`; select `cache.type: persistent`
for an additional per-ordinal PVC. Scratch is a separate bounded emptyDir at
`/scratch`; runtime paths are `/scratch/transcode` and `/cache/ether`. Neither
cache nor scratch replaces config identity storage. Set ephemeral-storage
requests/limits in `resources` and ensure node capacity covers cache plus scratch;
emptyDir limits are not reservations. Persistent cache changes require planning
because StatefulSet claim templates are immutable. Select this mode before
installation or migrate with a controlled drain/recreation retaining data PVCs.

Private mTLS listens on 32401 and advertises
`https://POD.RELEASE-ether-private.NAMESPACE.svc.cluster.local:32401`.
Set `clusterDomain` for other suffixes. The private headless service publishes
addresses before readiness and during drain so identity DNS and ongoing private
traffic remain possible. DNS resolution does not authorize placement: runtime
must reject startup/unready/draining nodes for new work.

Health probes use separate private HTTP port 32402: `/livez` for process health,
`/startupz` for completed provisioning, `/readyz` for service of existing work,
and `/admissionz` for admission of new work. A fresh clustered boot first
advertises its usable private protocol, requests a heartbeat, and keeps
`/readyz` unready, with diagnostic reason `capability_not_published`, until a
successful owned heartbeat records that exact boot's protocol capability. A local listener or
local protocol value alone cannot establish readiness. Failed or cancelled
publication, or conflict with another boot's membership, cannot supply that
receipt. Peer requests still check current database membership and authority;
the receipt is startup readiness evidence, not a cached authorization grant.
Scoped SQLite/PostgreSQL checks on published `6b6a2ab951` cover this receipt and
an actual replacement-boot conflict. They do not supply live Ingress or playback
acceptance for that runtime.

Self-readiness measures elapsed time
since this process completed its last owned heartbeat with Go's monotonic clock;
it never compares a database timestamp with local wall time. Established service
gets a 20-second grace after the heartbeat's 20-second freshness window expires.
Transient placement reconciliation failures receive the same 20-second grace. Listener/runtime failure, invalid roles or
loss of owned membership fail immediately. Pure delivery registers its empty
placement target at startup and does not repeat that write during playback.
Readiness grace only preserves routing: each media request, peer authorization,
placement and execution renewal still applies its current authority/fence checks.
A sustained database outage can therefore reject media before routing readiness
expires. Clock steps can still affect credentials, leases and remote membership;
this readiness change does not assert clock-independent distributed authority.

Public pod readiness uses
`/admissionz`; specialist readiness uses `/readyz`. During drain, readiness may
stay true while admission is false. Public endpoint removal is valid only with
the runtime's any-frontend session forwarding; validate that with real playback
before treating the cluster as continuously available. Do not expose probe or
private ports through an Ingress or public load balancer.

Pods run as UID/GID 999, with fsGroup, no API token, no capabilities, no privilege
escalation and a read-only root filesystem. The image's root/chown entrypoint is
bypassed. Your CSI driver must honor fsGroup ownership; a volume permission error
requires provisioner/volume ownership repair, not a privileged chart workaround.
Secret mounts are read-only 0440. Default service account permissions are not
needed. Optional NetworkPolicy restricts private traffic to this release and
public traffic to configured peers; it also requires explicit egress for your
database, upstream providers and remote APIs. Test on a CNI that enforces policy.
Kubelet probe treatment varies by CNI. DNS allowance assumes kube-system DNS;
NodeLocal DNS installations may need additional rules.

## Drain, upgrade and rollback

StatefulSets use **OnDelete**. `helm upgrade --wait` updates templates but does
not replace old pods or prove the new runtime works. There is intentionally no
automatic deletion hook. Back up the database, encryption key, authority and
individual config identities before upgrades. Follow release migration notes;
Helm rollback does not reverse database migrations or Secret changes.

For each pod, verify replacement capacity, then use:

```sh
packaging/kubernetes/rollout.sh ether ether-ether-full-0
```

The helper invokes `ether cluster drain --data-dir "$ETHER_DATA_DIR" --wait 120s` through
`kubectl exec`. This is a local owner-only request/status file protocol scoped
to the current boot and operation, not an unauthenticated network mutation. The
command stops new placements and waits for runtime quiescence. On timeout it
returns failure and the helper does **not** delete the pod; unresolved ownership
must remain fenced. If a backend's Close blocks, the runtime keeps service,
private membership and cleanup alive; a drain deadline does not report successful
retirement. Investigate a timeout rather than forcing deletion to advance a
rollout. After SIGTERM, reaching the runtime's drain deadline with unresolved
work logs that state and continues membership, existing-media service and cleanup
until proof arrives or external termination occurs. SIGTERM cannot cancel an
arbitrary backend Close and is not a substitute for explicit proof before
deletion. The chart sets a 180s termination grace period, above the default 120s
runtime drain deadline with additional shutdown overhead. When that grace
expires, Kubernetes eventually forces remaining container
processes to stop with SIGKILL. That kill does not prove source/encoder retirement
or free uncertain durable capacity; ownership remains uncertain until authority
and fencing evidence resolve it. Increase the grace period for your workload,
but do not treat any deadline as a retirement proof.

After successful drain the helper sends a Kubernetes DeleteOptions request with
an atomic UID precondition for the original pod. If a replacement races the
request, the API conflict aborts the helper; it does not retry deletion by name.
The helper preserves the normal pod grace period, waits for a new UID and Ready,
and leaves its PVC intact. Verify playback and authority before proceeding
to the next ordinal. If a candidate image fails, run `helm rollback ether REV -n
ether`, inspect the rendered old image/config, and use the same sequential drain
procedure. An already drained pod cannot receive new admissions until restart;
if deletion fails, complete the replacement deliberately. Never force-delete a
pod on a partitioned host until its process and source/encoder ownership have
been positively fenced. Do not delete a namespace/PVC as a rollback method.

## Reachability and GPUs

The API Service defaults to ClusterIP on 32400 and selects full/server pods.
With full nodes as the only public role, its existing LoadBalancer/NodePort option
and catch-all `/` Ingress remain unchanged. If server or delivery pools are
enabled, rendering requires an enabled Ingress with a nonempty hostname list,
API Service type ClusterIP, at least one API node (full/server), at least one
media node (full/delivery), and the explicit temporary
`ingress.canonicalHLSLabOnly=true` acknowledgement. That flag defaults false and
permits only isolated canonical-HLS lab staging, not mixed-client deployment. Direct L4 API exposure would bypass representation
routing and is rejected. Installing an Ingress object is not sufficient: provide
an actual controller, DNS/TLS and an externally reachable shared origin.

For mixed public roles the chart creates a ClusterIP-only `RELEASE-ether-media`
Service and configures these literal Kubernetes Prefix routes under that same
host, preserving paths and query strings:

| Prefix | Backend | Contract |
| --- | --- | --- |
| `/video/:/transcode/universal/session` | media (full/delivery) | Canonical representation namespace; unrecognized resources receive 404. |
| `/sessions/media` | media (full/delivery) | Canonical grant-bearing representations: GET/HEAD only. |
| `/jellyfin/sessions/media`, `/emby/sessions/media` | media (full/delivery) | Original mounted MediaBrowser media handlers; GET/HEAD and their OPTIONS preflight. |
| `/` and every other path | API (full/server) | Browse, admission, control and control-coupled responses. |

The controller must retain the media routes when their Service has no ready
endpoints. The pinned Traefik lab controller sets
`--providers.kubernetesingress.allowemptyservices=true`: an empty media backend
then returns 503 instead of dropping its route and sending the request to the
catch-all API backend. Other controllers need the same routing invariant. This
does not make an unready delivery node eligible or establish why readiness was
lost. See Traefik's [Kubernetes Ingress provider documentation](https://doc.traefik.io/traefik/reference/install-configuration/providers/kubernetes/kubernetes-ingress/).

Delivery accepts GET/HEAD representations and MediaBrowser alias OPTIONS
preflights. Other methods are not alternate API control routes. Prefix matching uses complete path elements, so similarly named
paths such as `/sessions/mediaevil` remain API traffic. Do not broaden either
prefix to `/video/:/transcode/universal` or `/library/parts`. Plex decision,
start/start.m3u8/start.mpd/start.mkv, ping/stop, `/transcode/sessions`, direct parts
and part-track PUT, streams, download queues and query-based subtitle endpoints
remain on API. Some GET endpoints both admit and stream bytes in one response;
server nodes may proxy those bytes through real remote adapters without local
providers or encoders. This topology does not claim that every byte path runs
on a delivery-only node or rewrites a client to a different media origin.

Writer-derived inventory distinguishes authored URLs from synthetic boundary
probes. Plex authors its MPD at `/video/:/transcode/universal/start.mpd` on API;
the resulting `session/{id}/{representation}/header` and numbered `.m4s` media
paths are already in typed delivery scope. ASS uses the query-bound
`/video/:/transcode/universal/subtitles` or
`/subtitles/:/transcode/universal/start` API endpoints, outside the media Prefix.
Plex does not author `session/id/subtitles/1.ass` or `session/id/base/index.mpd`.
Those synthetic requests do match the broad Prefix and correctly receive 404 at
current delivery; they do not establish an authored compatibility defect or an
application-to-Ingress fallback guarantee.

The explicit lab gate remains because mixed deployment still needs writer-derived
URL/auth fixtures, actual same-origin controller tests and protocol capability
acceptance. It is not justified by invented representation URLs. No controller
CRDs, suffix widening, URL rewrite or authentication relaxation are added.
Full-only public pools remain supported.

Jellyfin/Emby media aliases use the same stateless SQL-owner hop and generation
fencing as canonical media. The private receiver replays the original mounted
frontend; it preserves the complete path/query, bounded account/device headers,
origin and representation headers. The shipping broker still authenticates the
path grant, protocol, active viewer and current policy on every request. Account
credentials (`api_key`/`ApiKey`, token headers and MediaBrowser/Emby authorization
headers) retain their original precedence and authority; none replaces the path
grant. Emby cache/CORS and both protocols' media-safety headers are preserved.
Their ordinary OPTIONS response comes from the original frontend. Direct
`/Videos/...`, lazy admission and control routes remain on API. Silo playback
paths likewise remain on API until their authenticated seam is implemented.
Full nodes publish the exact local source owner before returning a MediaBrowser
session; remote-source sessions publish their existing gen-zero route at Open.
Standalone brokers do neither. No Ingress path rewrite or broad protocol
catch-all is used.

Select the controller class, hostname, TLS Secret and controller-specific stream
timeouts. For the explicitly gated HLS lab, the controller must accept the literal colon
in Plex paths and honor longest Prefix routing without URL rewriting. Test actual range/HEAD requests,
WebSockets, long streams, auth/policy and proxy buffering against that controller.
Console routing remains separate from this public Plex listener; this chart does
not publish a console service.

Kubernetes bridge networking does not supply LAN GDM UDP discovery or UPnP router
mapping. Configure reachable advertised connections and explicit router/firewall
mapping for remote clients, and verify from a real external network. A hostname
or Ingress alone does not solve Plex client reachability. `plex.direct` names
encode the advertised address and server identity; account for DNS-rebinding
protection, certificate hostnames and public/private address selection. Do not
route a private pod-DNS URL to a Plex client.

Overlay `packaging/kubernetes/nvidia.values.yaml` for an NVIDIA transcode pool.
The host needs a supported driver, NVIDIA Container Toolkit and device plugin
(or GPU Operator); the chart installs none of these. Label suitable nodes
`ether.plex/gpu=nvidia`. Request `nvidia.com/gpu: 1` and optionally select an
administrator-created RuntimeClass. For NVIDIA GPU limits the chart requests `NVIDIA_DRIVER_CAPABILITIES=compute,video,utility`,
including the video libraries required by NVENC/NVDEC. CDI installations must
also include video libraries in their generated device specifications; verify
this with the cluster administrator. See [NVIDIA runtime capabilities](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/docker-specialized.html).
A resource request schedules hardware; it
does not prove FFmpeg can encode. Verify the device, available encoder and an
actual host workload before claiming acceleration. `resourceScope` and capacity
must represent physical capacity correctly if multiple workers share a device;
independent node defaults must not double-count the same GPU. CPU fallback is
the mixed CPU example: omit GPU limits/runtime selectors and provide CPU headroom.
Failure to schedule a GPU pod does not automatically reschedule it as CPU.

## Troubleshooting and acceptance

Pending PVC: inspect StorageClass, access mode and provisioner events. Pending
GPU pod: inspect allocatable GPU resources, labels, taints and RuntimeClass.
Startup probe failure: inspect Secret key names/modes, PostgreSQL TLS/session
pooling, clock-bound assertion, issuer/roots consistency and pod-DNS reachability.
Never paste credentials or node identity files into a ticket. `CrashLoopBackOff`
on an old image usually indicates a missing deployment runtime contract. A
healthy process with admission false may be draining or lack available capacity;
restarting does not prove an old encoder has stopped.

`packaging/kubernetes/check-chart.py` checks rendered role, storage, Secret,
probe and invalid-value contracts. CI lints, checks and packages a chart artifact;
release publication is a separate coordinator action. Use
`packaging/kubernetes/acceptance.sh` only in an isolated test namespace with a
real prepared playback workload. It records infrastructure identity/restart
proofs and calls your workload verifier before/during/after failure. That verifier
must measure real stream continuity, ranges/seeks, fencing and source/encoder
retirement. A successful render, readiness probe or synthetic HTTP response is
not playback acceptance. Include full+transcode+delivery, then source, and test
hard loss separately from graceful drain. Do not use an owner's production
Plex claim or provider credentials for acceptance.

WSL GPU/container availability and native Windows NVENC availability are separate
capabilities. A native Windows transcode specialist can join over authenticated
private transport and use the host driver without a Kubernetes GPU pod. Its
private HTTPS endpoint and database must be reachable by the cluster, with the
same trust, identity, clock and capacity rules as container workers. The lab
`packaging/kubernetes/native-launcher` contains Ether and its FFmpeg descendants
in a kill-on-close Windows Job Object; terminating that owned launcher bounds
child cleanup. It is a lab launcher, not a Windows service installer. A native
probe does not prove distributed placement or loss recovery. Do not change
WSL/host drivers or the shared Docker daemon as part of chart installation.

Transcode placement first requires a live eligible incarnation, available
capacity and a successful exact-request capability probe. It then ranks verified
hardware encoding ahead of software, followed by spare slots (the bottleneck
across required resource scopes), source-node locality and stable node/boot
identity. Capacity reservation rechecks atomically, including displaced claims.
An exhausted or incapable GPU can fall back to an eligible CPU worker; an
unschedulable GPU pod itself is not converted into a CPU pod. Shared-device
workers must share a physical resource scope. Keep enough eligible CPU capacity
to recover when the only GPU specialist is lost.

See [the complete values reference](kubernetes-values.md).

References: [StatefulSet identity and retained volumes](https://kubernetes.io/docs/concepts/workloads/controllers/statefulset/),
[Helm chart schemas](https://helm.sh/docs/topics/charts/),
[Kubernetes device plugins](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/),
[NVIDIA device plugin prerequisites](https://github.com/NVIDIA/k8s-device-plugin#prerequisites).

For independent API validation, create an isolated `ether035-ha` namespace, set
`KUBECONFIG` to the lab kubeconfig, and run
`packaging/kubernetes/api-check.sh ether035-ha`. This uses the exact mixed-role,
persistent-cache, Ingress and NetworkPolicy fixture and applies only a server
dry run. It does not start workloads or need credential Secret bytes.

For a multi-node kind lab, `packaging/kubernetes/kind.yaml` creates one control
plane and two workers. The explicitly isolated hard-loss harness is:

```sh
packaging/kubernetes/hard-loss.sh ether035-ha ether035-ha-worker \
  /path/to/real-playback-verifier /tmp/ether035-hard-loss-evidence
```

It captures the kind cluster/worker labels and exact container ID together in
one inspection, then uses only that immutable ID for subsequent Docker operations.
It stops only that worker, checks Docker reports `running=false` and `pid=0`, and restores it
on exit. Its verifier stages (`before-loss`, `during-loss`, `after-loss`,
`after-recovery`) must demonstrate ongoing playback, authority/fencing and
retirement evidence while the node is actually stopped. Place redundant public
and worker capacity on other nodes before running it. A single-worker GPU pool
cannot maintain a hardware transcode when its sole device is lost. Do not
substitute a readiness-only or fabricated response verifier. A failed verifier
means acceptance failed, even if the node restarts.

`python3 packaging/kubernetes/check-rollout.py` runs offline command stubs for
drain failure, a replacement racing the UID-preconditioned delete, successful
replacement, and container-name reuse safety. These checks run in chart CI and
do not count as playback or hard-loss acceptance.

### Generation checks during media bodies

Every gateway, worker, source and delivery response admits output with a full
current durable generation check before headers. `generationfence.Writer` shares
one request-local authority observation between header, read, write and flush
checks. It repeats durable validation after at most one second of monotonic time
(measured from the start of validation), independent of body size. A positive check
that exhausts that interval or races invalidation is discarded and retried,
without publishing between attempts. A single resync or slow observation does
not revoke a healthy writer. At most three attempts are made; each uses a fresh
start time and event epoch and must complete inside its observation interval.
The callback's host context/I/O deadline bounds each call; the attempt limit
bounds retries. Negative authority, failed local checks or exhausted attempts
latch denial. Source handlers may request a shorter observation interval.

Between durable checks every operation checks local cancellation, sealing and
monotonic execution/grant deadlines where present. Delivery converts its observed
placement deadline to a monotonic cap. A blocked producer returning after a cap
or the revalidation interval cannot bypass these checks. An already admitted
socket write cannot be recalled; an idle writer does not poll or retain an extra
goroutine and revalidates before its next output operation.

Committed route updates publish the session ID on the existing cluster bus as
`playback.route`. Active writers subscribe only for their request lifetime;
route notifications and bus reconnect/resync invalidate their observations.
Callbacks perform only local invalidation, so the next output operation performs
the durable check. This normally detects supersession at the next write, within
milliseconds for an actively streaming producer. Lost notifications retain the
one-second bound. Notifications never grant authority or release resources.

This is bounded streaming staleness, not instantaneous distributed revocation.
Replacement output starts at the first unavailable fixed-grid segment N under the
next generation; every new segment request validates current authority before
headers and a response remains bound to its admitted generation. No request
switches encoder generations in the middle of its body. Existing in-flight
responses can emit old-generation bytes within the bound; after denial clients
must retry through current authority. Output retention/publication keeps its own
durable checks. This optimization alone does not prove client-side zero stalls,
splice continuity or exact audio continuity; those remain acceptance gates.

The DB cost of streaming validation is O(requests + active streaming seconds),
plus invalidation events, rather than O(body writes). Request admission remains
durable. The gateway reads sequential source ranges of at most 2 MiB, while
keeping exact-offset whole-range retries on source failure. Scratch is bounded
by 2 MiB per active response (less for short ranges). This replaces a source
HTTP admission on every 64 KiB. At 20 Mbps it reduces admissions from about
38.15/s to 1.20/s, measured with a 150 MB offline transfer representing 60 seconds
of media. Each range retains grant verification, exact-source acquisition and
output admission: at least nine core SELECTs, plus membership/viewer policy SQL
and transaction overhead. The measured core admission contribution falls from
343.35 to 10.80 SELECTs/s; slower source responses can also require periodic
streaming checks. A writer-only benchmark must not be reported as total source
pipeline query rate. Small client ranges and random seeks still pay per-request
admission; the sequential rate assumes a continuous encoder input request.

## Playback coordinator and delivery loss

Overlay `packaging/kubernetes/routing-ha.values.yaml` on mixed-role values for
two server and two delivery replicas on distinct Kubernetes hostnames. Supply
the existing operator-managed Secrets, database and same-origin Ingress. The
API Service selects **all** full/server replicas through `ether.plex/public`;
the media Service selects all full/delivery replicas through `ether.plex/media`.
Neither selector pins a particular ordinal or elected global leader. The
headless private Service resolves exact source/encoder incarnations; balancing
those RPCs across a role Service would discard their execution identity.
Existing readiness probes and per-pool PDBs apply. Hard loss requires spare
ready replicas; a PDB constrains voluntary disruption only.

Delivery retains no authoritative output or coordination. Every encoder-media request resolves the current execution generation from SQL
and forwards over authenticated node RPC to an available server replica. A failed
connection can retry another replica before response headers. Preparation-only
direct media retains its source/controller route.
The receiving original protocol handler rechecks viewer/grant, device and media
policy. Range, conditional requests, HEAD and cancellation follow that handler;
redirects and additional delivery hops are refused. Output lives at the encoder;
optional controller retention is a second copy, never delivery's unique data.

Playback coordination uses a separate SQL lease and monotonically increasing
epoch (`clusterplayback/6`). Its two-second base lease is extended by twice the
configured clock-skew bound. A surviving eligible server claims an expired
lease by CAS,
observes the exact healthy worker through status (no Start), and renews its
placement, source and worker leases. The execution fence/generation and encoder
process remain unchanged. Worker requests bind the current certificate and
coordinator epoch; a stale controller cannot stop or renew that execution.
Source movement keeps its independent source generation. Global cluster
leadership and registrar epochs are unrelated to either playback generation.

The original Plex stream binding, read capability and source authorization
snapshot are persisted in the shared database before publishing the HLS master.
These are credentials: do not export the binding table in evidence or logs.
Surviving replicas restore protocol facts and run the original live policy
checks. Binding snapshots are bounded by admitted placement history and become
unreadable when that execution is retired or loses authority. They do not
manufacture a new source owner or encoder start. A repeated decision on the
surviving coordinator can reuse the source manager’s exact protected lifetime;
obsolete preparations cannot close it, and plan cleanup cannot retire a source
while the healthy execution still owns its binding.

The in-process `TestDeliveryAndControllerLossKeepExecution` stops delivery and
controller HTTP listeners, waits for the real coordinator lease, and verifies
segment response continuity, Range/HEAD/416 parity, timeline availability,
renewal and one encoder start. Its media backend is an offline fake. It does
**not** certify decoded playback, real Ingress endpoint removal, a client buffer,
PostgreSQL availability or physical node loss. Run the acceptance collector
across each actual loss before release. Media reads do not wait for the coordination lease: a surviving server restores
the original protocol authorization snapshot, checks the live viewer/grant/policy,
and reads the healthy encoder under its exact execution generation. Read-only
worker RPC credentials allow status, inspection and representations; start, stop,
renewal and playback mutations still require the current coordinator epoch.
Media restoration does not claim the public registry row.

Coordination detection is bounded by the two-second lease, three clock-skew
bounds, the one-second control tick and SQL/RPC latency. With a one-second
pairwise clock bound this is at most six seconds plus I/O. Stop/progress requests
wait and retry during election, bounded by that lease plus three skew bounds and
four seconds of I/O headroom (or the client deadline). They never acknowledge
a queued Stop before the coordinator executes it. Expiry transfers control only;
a healthy execution retains its generation. Database or worker outages beyond
these bounds can still return an error and retain responsibility.


## Specialist roles and recovery boundaries

The role presets are deployment-time responsibilities. Empty runtime roles mean
`full`, including server, source, transcode, delivery and background duties;
explicit role arrays compose only the selected responsibilities. Specialist
roles require PostgreSQL. Change a pool's role by a reviewed deployment change
and drain/replacement, not by copying identity files or changing a running node.

| Helm pool | Responsibility | Public routing |
| --- | --- | --- |
| `pools.server` | Protocol admission, browse/control, session coordination and lease renewal; remote source/encoder adapters | API Service |
| `pools.source` | Provider acquisition and exact-release byte-range serving | Private transport only |
| `pools.transcode` | Worker-local input gateway, FFmpeg and segment output | Private transport only |
| `pools.delivery` | Stateless representation forwarding through any available server to the SQL-fenced encoder | Media Service |
| `pools.full` | All responsibilities, including background work | API and media Services |

A four-specialist lab configuration is shown in
[the values reference](kubernetes-values.md#four-specialist-pools). Use at least
two eligible instances per role in separate failure domains and surviving
PostgreSQL authority. Spare capacity must cover the failed execution. Replicas
on separate Kubernetes nodes on one machine do not prove physical-host HA.

Source loss is handled beneath FFmpeg by `internal/nodeinput`. The gateway
retries an exact byte range through another eligible source, which reopens the
same release and validates revision and size. Source route CAS advances an
independent source generation, fencing old reads without restarting a healthy
encoder. The current implementation permits at most three source moves per
encoder generation and needs a persisted exact-source recovery descriptor;
an arbitrary local source without that descriptor cannot be recovered remotely.

Encoder loss advances the execution generation and resumes from the first
segment beyond delivery's durably published contiguous prefix. Cached published
segments remain serviceable under current authority. The replacement uses the
same title-time segment grid, start number and output timestamp offset, with a
forced IDR and closed GOP at each boundary on supported re-encode paths.
Generation checks reject a stale encoder's publication; lease-renewal failure
past the accepted monotonic deadline self-fences the worker. Displaced capacity
stays charged until exact retirement proof. Node or pod status can accelerate
failure detection but never substitutes for these authority checks.

The private recovery buffer retains complete immutable segments even when their
HTTP response carries `Cache-Control: no-cache`. Replay still validates current
authority and preserves the original client-facing cache policy; `no-store`
continues to forbid retention. Successfully retained contiguous segments advance
the published floor, so recovery starts at the first segment beyond that floor.

An authenticated replacement boot for the same transcode node ID triggers
encoder recovery immediately, without waiting for the transport-failure grace.
The coordinator serializes this decision with ordinary dead-boot retirement.
For a recoverable active session, only the supersession transaction seals the
old operation gate and advances the generation; temporary lack of spare capacity
retains that gate for retry. Dead-boot reconciliation then frees the displaced
old generation's claims. Cancellation or an unsupported recovery plan can retire
the current session after independent source retirement proof. Missing local
coordinator memory alone never proves that the viewer abandoned playback.

### Automatic recovery admission

Admission capabilities below describe the published implementation, not measured
acceptance of a deployed build. Every recovery uses the durable contiguous
published floor, generation fencing and independent retirement proof.

| Mode | Current admitted scope |
| --- | --- |
| Encoded video with copied audio | HLS MPEG-TS video transcode, zero initial offset, no live input, soft subtitles or pre-existing `RecoveryStart`; the admitted authored ramp is preserved |
| Re-encoded audio | CPU H264 output plus re-encoded AAC from AC3 at 48 or 44.1 kHz; known positive-duration, constant-frame-rate video; durable nonnegative decoded-padding fact; steady plan; output stereo 48 kHz (or those defaults) |
| Video/audio copy | Only the authenticated, completely verified version-1 H264/AAC MPEG-TS copy timeline described below |

Re-encoded-audio recovery rejects ramps, audio filters, slate, subtitle burn-in,
tone mapping and enhancement. Automatic ramp authoring skips re-encoded audio;
the recovery gate requires a steady plan. Copied AAC requires exact ordered AU
identity; independently encoded AAC instead requires the sample-clock and PCM
continuity checks in [splice verification](splicecheck.md#re-encoded-aac).
An offline audio proof does not establish real-cluster recovery acceptance.

With shared FastStart enabled, the server can author a versioned HLS ramp for
eligible copied-audio H264 transcodes before durable admission. It freezes the
ramp segment count/duration and steady timeline, so replacements do not reread
mutable worker settings. Ramp authoring allows TS/fMP4, but the automatic
encoded-recovery gate above remains MPEG-TS. The private specialist configuration
no longer unconditionally disables FastStart. Adaptive ladders, their copy-path
variant, the spike governor and RTX Video enhancement remain disabled there.
Combined ramp/re-encoded-audio recovery is outside the admitted profile.

Copy recovery requires a complete source-probed timeline, not metadata supplied
by a client. The bounded version-1 profile has exactly two MPEG-TS streams:
25 fps H264 without reordered frames and stereo AAC-LC at 48 kHz. Closed-GOP IDRs
with SPS/PPS every 48 frames align with 90 AAC access units on a 1.92-second grid.
The entire title must be an exact multiple of that grid, longer than one segment
and no longer than 120 seconds. Full packet and decoded-frame inspection must
succeed; source-owner authentication binds the fact and keyframe array to the
pinned revision before input is issued. Initial and replacement commands both
copy video and audio. Unknown, long, irregular, open-GOP, reordered or unaligned
sources remain outside this recovery scope. Ordinary playback and copy seeks do
not gain a recovery guarantee from an absent proof. See the
[copy recovery contract](contracts/private-copy-recovery.md).

Controller takeover and stateless delivery behavior are described above; neither
moves a healthy encoder merely because a public replica disappears.

Membership `last_seen` writes and age calculations both use PostgreSQL's clock,
so a node wall-clock step cannot manufacture membership freshness. Membership
liveness remains distinct from lease authority and confirmed process retirement.
Host NTP is mandatory even with DB-clock membership and continuous skew checks.
Clock checks derive wall and monotonic readings from one observation. In-bound
corrections are logged without quarantine; measured skew plus uncertainty must
remain inside each node's half of the configured pairwise bound. Clock quarantine
blocks new authority and lease extension, while a healthy running encoder remains
observable under its accepted lease. Recovery probes check exact membership and
encoder status, independently of eligibility for new work. Recovery decision logs
name the detector, probe duration/deadline, failure grace and consecutive-failure
count. Status observation has a two-second RPC deadline. Transport loss needs
at least three consecutive failures and four seconds since the first failed
probe completed; any healthy probe resets both. Authenticated replacement boot,
explicit TLS identity/policy denial, HTTP 401/403/410 status refusal and confirmed
encoder retirement bypass this grace. None of these observations alone releases
old capacity or proves process retirement.

For one session with an available controller ticking every second, a silent
hang is detected conservatively within ten seconds plus control SQL/scheduling
latency: up to one second to the next tick, two seconds for the first probe,
four seconds of grace, and up to one tick plus one probe (three seconds) to
observe expiry. The three-failure minimum fits inside this bound. The runtime
has a three-second operation budget per tick; overloaded SQL or many serialized
sessions can delay progress further, so this is not a universal wall-clock SLA.
Explicit loss signals take the next successful probe without the four-second
wait. Placement and replacement startup add their own recovery time.

The shared-host lab's eight logged failed/decision probes had p50 500.582 ms,
p99/max 501.244 ms, including six 500 ms timeouts. Before the fault, only two failed probes were
logged: p50 500.760 ms, p99/max 501.104 ms. These are failure-only,
timeout-censored observations, not healthy RPC percentiles. The two-second
budget exceeds three times that observed p99; the three-failure minimum prevents
two isolated slow observations from triggering recovery. Enable debug logging
for `encoder recovery probe` to measure the complete healthy/failing distribution
in subsequent labs. Recalibrate against those uncensored observations.

The bounded recovery store releases a session slot only after exact durable
retirement and after pending retention/readers drain. Maintenance retries pinned
slots; no active published prefix is evicted to admit another session. MPEG-TS
inspection accepts H.264 PES with omitted packet length (zero), delimited by the
next payload-unit start or segment end; AAC still requires its explicit length.
This permits the real generated-media pipeline to publish its contiguous prefix
and restart at floor+1.


## Known limits and release gates

Use the bundled FFmpeg (pinned 8.1.3). Stock 8.0.x has fMP4 copy-path failures
that reproduce on its release baseline. Capability changes and package/PG gates
are separate from deployed playback acceptance; every new runtime needs its own
placement, client, control and retirement evidence.

The v0.3.5 loss matrix passes: all 25 software rows (Plex source, transcode,
delivery and controller loss by force-delete and pause at startup and steady
state; Jellyfin transcode and controller loss; an Emby baseline) and the five
mode gates (copied-AAC ramp loss at segments 1, 2 and 3, AC3-to-AAC steady
recovery, bounded-copy steady recovery), each with zero client stalls, all eight
media checks, successful controls and cleanup inside 15 s. The table, runtimes
and fixture limits are in
[the acceptance guide](kubernetes-acceptance.md#v035-matrix-2026-11-07).
Native GPU encoder loss with CPU recovery and physical host power loss are not
measured. Local NVENC restart-clock and software splice proofs remain scoped to
their recorded tools and media.

See [the acceptance guide](kubernetes-acceptance.md) for component verdict rules
and the private evidence-index contract. The chart's mixed-role lab
acknowledgement remains required. Live reports and their hashes belong in
thread storage, not this repository.


Public distributed Stop removes local session admission and seals the exact
execution's cancellation without contacting the encoder. Its SQL seal attempt
has a 750 ms context budget; maintenance retries an uncommitted local stop
intent. Public session handles, including remux/unbuffered representations,
route retirement through this coordinator path. Healthy database/control
routing is still required for an HTTP acknowledgement and durable cross-replica
cancellation; a client timeout during database loss is not proof of a seal.
Delivery refuses cancelled operation gates. The controller maintenance tick
performs bounded worker stop/status RPCs, retaining claims, input ownership and
output bytes until exact retirement and reader drainage are proven. A paused
encoder therefore cannot hold up the public Stop response, and a quick Stop
response does not claim that the paused process has exited.


Media discovery prefers the current playback coordinator when it remains a
published server replica. Its complete segment reads populate the recovery
store before forwarding finishes. Serving through a read-only replica while
that coordinator is alive would bypass capture and leave its published floor
empty. If the coordinator is unavailable, the existing transport fallback still
serves through another published server without waiting for election; this does
not manufacture a retained prefix on the unavailable coordinator.

Explicit TCP connection refusal, absent DNS name, host/network unreachable and
HTTP 404 on the private status endpoint trigger replacement without the
transport-failure grace. Timeouts and unclassified transport failures retain
the grace and consecutive-failure threshold. Endpoint absence is a replacement
signal only: displaced claims remain until independent retirement proof.
