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

`full` maps to the runtime’s empty roles array and runs all roles; `server` exposes the public frontend; `source`, `transcode`
and `delivery` are specialists without public Plex listeners. Enable the pools
needed for your topology. Public service selectors only match full/server pods.
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

## Storage, probes and transport

Every ordinal gets a retained `data` PVC mounted at `/config`. Cache defaults to
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
and `/admissionz` for admission of new work. Public pod readiness uses
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

The helper invokes `ether cluster drain --data-dir /config --wait 120s` through
`kubectl exec`. This is a local owner-only request/status file protocol scoped
to the current boot and operation, not an unauthenticated network mutation. The
command stops new placements and waits for runtime quiescence. On timeout it
returns failure and the helper does **not** delete the pod; unresolved ownership
must remain fenced. Private listeners, membership and retirement proofs must
survive the drain until completion. Investigate a timeout rather than forcing
delete to make a rollout proceed. SIGTERM uses bounded runtime drain, but is not
a substitute for this explicit proof before deletion. The chart sets a 120s
termination grace period and does not claim a Kubernetes grace deadline proves
retirement. Increase it for your workload as appropriate.

After successful drain the helper deletes only that pod, waits for a new UID and
Ready, and leaves its PVC intact. Verify playback and authority before proceeding
to the next ordinal. If a candidate image fails, run `helm rollback ether REV -n
ether`, inspect the rendered old image/config, and use the same sequential drain
procedure. An already drained pod cannot receive new admissions until restart;
if deletion fails, complete the replacement deliberately. Never force-delete a
pod on a partitioned host until its process and source/encoder ownership have
been positively fenced. Do not delete a namespace/PVC as a rollback method.

## Reachability and GPUs

The public Service defaults to ClusterIP on 32400. A LoadBalancer or an optional
Ingress exposes only full/server pods. Ingress routes `/` with Prefix; select a
controller class, hostname, TLS Secret and controller-specific streaming timeout
annotations. Test range requests, WebSockets, long streams and proxy buffering
against that controller. Console routing remains separate from this public
Plex listener; this chart does not publish a console service.

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
administrator-created RuntimeClass. A resource request schedules hardware; it
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

It verifies the kind cluster label, records the exact container ID, stops only
that worker, checks Docker reports `running=false` and `pid=0`, and restores it
on exit. Its verifier stages (`before-loss`, `during-loss`, `after-loss`,
`after-recovery`) must demonstrate ongoing playback, authority/fencing and
retirement evidence while the node is actually stopped. Place redundant public
and worker capacity on other nodes before running it. A single-worker GPU pool
cannot maintain a hardware transcode when its sole device is lost. Do not
substitute a readiness-only or fabricated response verifier. A failed verifier
means acceptance failed, even if the node restarts.
