# Ether on Kubernetes

This chart packages the v0.3.5 deployment runtime. Use an image that implements
`ETHER_DEPLOYMENT_FILE`, role isolation, authenticated private transport and the
probe/drain contract below. Older Ether images are incompatible. At the time of
chart preparation, the runtime accepts full nodes and a guarded transcode
implementation; source, delivery and server-only graphs remain gated. The mixed
role example is an acceptance target, not a claim that those roles already work. Chart rendering
checks do not establish playback continuity; that requires the acceptance run.

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
verified error bound, and an external PostgreSQL database reachable from every
pool. Select an explicit immutable image tag or digest before installation.

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
remove a runtime guard or establish that its shipping graph passes acceptance.
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

## Durable ledger quota

`runtime.capacity` limits concurrent work at a node/resource scope (1–4096).
`runtime.ledgerCapacity` is the separate cluster-lifetime quota for durable
session-generation floors: default 1000000, range 4096–10000000. It is at least
both concurrent capacity and the fixed 4096 boot-history bound. Every replica
must use the same ledger value. This chart requires the runtime version that
implements this field; earlier staging candidates reject it as unknown.

Placement seeds or verifies a persisted fleet policy under its transaction lock.
Each new Reserve checks equality with that policy in the same transaction.
Changing Helm configuration alone never silently raises the quota. When the
ledger fills, preserve its generation floors; there is no TTL deletion or
compaction that makes an old generation safe to reuse. Admission status and
transition logs expose exhaustion. Existing work must remain serviceable when
new allocations fail closed.

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
rows or deleting generation history. Plan this admission interruption before
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
and `/admissionz` for admission of new work. Self-readiness measures elapsed time
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
| `/` and every other path | API (full/server) | Browse, admission, control and control-coupled responses. |

The controller must retain the media routes when their Service has no ready
endpoints. The pinned Traefik lab controller sets
`--providers.kubernetesingress.allowemptyservices=true`: an empty media backend
then returns 503 instead of dropping its route and sending the request to the
catch-all API backend. Other controllers need the same routing invariant. This
does not make an unready delivery node eligible or establish why readiness was
lost. See Traefik's [Kubernetes Ingress provider documentation](https://doc.traefik.io/traefik/reference/install-configuration/providers/kubernetes/kubernetes-ingress/).

The delivery handler rejects non-GET/HEAD requests; they are not alternate API
control routes. Prefix matching uses complete path elements, so similarly named
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

Jellyfin/Emby media aliases require the explicit
auth-preserving runtime adapter and matching approved routing before being
claimed; the chart currently leaves `/jellyfin/sessions/media` and
`/emby/sessions/media` on API. Silo playback paths likewise remain on API until a
typed authenticated seam is implemented. Do not add stripping/rewrite annotations
or generic alias prefixes to imply unverified compatibility. Manual lab replay of
the same returned path from server pod to delivery pod proves that role path
only; it does not replace a real client test through the shared Ingress origin.

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

The initial GPU-workstation inventory found Docker usable, but NVIDIA NVML GPU
access blocked by the host OS and no NVIDIA Docker runtime. CPU chart acceptance
is possible; RTX4080 hardware acceptance remains a separate gate. Do not change
WSL/host drivers or the shared Docker daemon as part of chart installation.

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

Delivery retains no authoritative output or coordination. Every request resolves
the current coordinator from SQL and forwards once over authenticated node RPC.
The receiving original protocol handler rechecks viewer/grant, device and media
policy. Range, conditional requests, HEAD and cancellation follow that handler;
redirects and additional delivery hops are refused. Output lives at the encoder;
optional controller retention is a second copy, never delivery's unique data.

Playback coordination uses a separate SQL lease and monotonically increasing
epoch (`clusterplayback/6`). Its 15-second lease includes twice the configured
clock-skew bound. A surviving eligible server claims an expired lease by CAS,
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
across each actual loss before release. Budget at least the coordination lease,
three clock-skew bounds, the control tick and actual RPC latency within the
measured client forward buffer; configurations with larger skew need more buffer.
