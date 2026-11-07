# Isolated Kubernetes lab with an optional second physical host

This lab uses k3s-in-Docker, a dedicated Docker bridge (MTU1280), WireGuard
Flannel (measured MTU1200), pod CIDR10.65.0.0/16 and service CIDR10.66.0.0/16.
No host network namespace, host sysctls, service units, Docker daemon changes,
external accounts or real providers are needed. Existing kind labs are untouched.

The initial topology is one control-plane/server container plus two agent
containers on one physical host. Each node is capped at2GiB (6GiB aggregate).
This topology tests node/container failure, **not physical-host failure**.
PostgreSQL and generated NNTP/Newznab fixtures remain on the control node.
The chart runs two replicas of each singleton specialist role across agents;
PVCs retain each ordinal's identity. All server replicas share PostgreSQL.
A singleton control plane and PostgreSQL are not infrastructure HA. Stopping
that host cannot establish database availability.

## Provision and deploy

Use a private absolute state directory outside Git; set `umask077`. Write a
random shared join token to `$state/server/token` without printing it. Then:

```sh
packaging/kubernetes/multihost/node.sh server "$state/server" "$TAILSCALE_IP"
packaging/kubernetes/multihost/local-agents.sh "$state"
```

Save the server's `/etc/rancher/k3s/k3s.yaml` to `$state/kubeconfig`, mode0600;
change its API origin to `https://$TAILSCALE_IP:16443` and rename the context
`ether035-multihost`. Put pinned kubectl1.34+ and Helm3.19+ in `$state/bin`.
Python3/PyYAML, OpenSSL, Docker and the repository's Go toolchain are required.
Verify host synchronization (including offset/error bound) before using the
values' explicit1000ms clock assertion. The deployment intentionally refuses
unknown clock bounds.

One command builds a reviewed integration commit using niced pure Go, packages
the pinned FFmpeg bundle, imports it into all three owned local nodes, and
installs/updates the fixtures and chart:

```sh
packaging/kubernetes/multihost/heavy-io.sh \
  packaging/kubernetes/multihost/redeploy.sh "$state" "$INTEGRATION_SHA"
```

The chart uses OnDelete. That command updates templates without terminating
healthy processes. Drain and replace one pod at a time using
`packaging/kubernetes/rollout.sh ether035-ha POD` with the lab kubeconfig.
For initial staging of rejected/never-started pods only, the explicit
`role-loss.sh` helper performs UID-preconditioned abrupt replacement.

Expose ingress through `http://$TAILSCALE_IP:18035`, Host
`ether035-roles.test`. For seed setup, use a temporary loopback port-forward
straight to a server pod's32400 listener (owner network admission intentionally
rejects console setup through an untrusted proxy). Run:

```sh
KUBECONFIG="$state/kubeconfig" python3 packaging/kubernetes/seed-lab.py \
  --context ether035-multihost --base http://127.0.0.1:18040 \
  --credentials-dir "$state/playback-private" --kubectl "$state/bin/kubectl"
```

This seeds only generated fixture sources and title1000000000900001. Delegation
expires after15minutes; refresh before measurements. Keep all credential files,
grant URLs, media logs and evidence outside Git. Use the actual Plex
`decision -> start -> authored manifests -> segments -> stop` flow through
ingress. A bare Start baseline does not establish decision/start compatibility.

## Native worker access

Optionally write the first host Tailscale IP to `$state/external-private-ip`.
The Helm post-renderer exposes each role ordinal's private TLS origin through
its own NodePort. Run `relay.sh "$state" "$TAILSCALE_IP"` once. It creates a
64MiB capped container on the owned bridge with at most64 connections:

| Listener | Destination |
| --- | --- |
|15432|Lab PostgreSQL NodePort30432|
|18410,18411|Server ordinals0,1|
|18420,18421|Source ordinals0,1|
|18430,18431|Transcode ordinals0,1|
|18440,18441,18442|Delivery ordinals0,1; optional second-host ordinal2|

Private TLS remains end to end, including CA verification, actual IP SAN,
client certificates and cluster identity/boot checks. The relay holds no
credentials. Keep tailnet access restricted to authorized lab machines.
Transfer DB URL, cluster key and independent issuer/roots through authenticated
private file access; never paste them into commands or logs. Native workers
must measure their own PostgreSQL clock offset and round-trip error against
the configured bound before joining. Readiness alone does not prove this.

## Join another physical host

Provision only one2GiB agent there; no host networking/sysctl/systemd changes.
The host needs cgroupv2, Docker, in-kernel WireGuard, outbound API16443/tcp,
bidirectional51820/udp over Tailscale, and a1280-byte underlay. Native host-gw
is unsuitable across separate Docker bridges; published WireGuard endpoints
use `node-external-ip` and `flannel-external-ip` instead.

Copy the scripts and shared token privately to the second host, then run:

```sh
python3 pressure-gate.py --state "$remote_state" \
  ./join.sh "$remote_state" "$REMOTE_TAILSCALE_IP" "$SERVER_TAILSCALE_IP"
```

The guard samples `/proc/pressure/memory` every60seconds, requires full avg10<5
for30continuous minutes, and then starts one capped lab agent. It keeps checking
and stops that exact container if full avg10>10 or the guard exits. Preserve
`pressure.jsonl` as evidence. A failed clock gate remains a separate blocker.
A native Windows transcode worker is independent of the WSL agent; its own
measured clock/pressure gates and coordinated run window still apply.

Import the exact integration image into that node before scheduling workloads;
`docker save` plus `docker exec -i NODE_ID ctr -n k8s.io images import -` requires
no registry or daemon changes. The second host receives only `ether.lab/delivery=true`; keep the general
`ether.lab/workload` label off it so source/server/transcode remain local.
Copy `physical-host.values.yaml` into the private state directory, label both
local agents `ether.lab/delivery=true`, then run install/redeploy. The overlay
adds one delivery ordinal and spreads only delivery pods across the three
agents. Other pools continue to spread across the two local agents. Existing
per-ordinal PVC affinity remains intact during replacement.
Keep the second host limited to one light role (prefer delivery), with no
transcode there. A third delivery ordinal can use fresh local storage there,
while the two existing local ordinals retain their PVCs. Verify actual placement
across `ether.lab/host` for a physical-host experiment; the default hostname spread alone does
not guarantee cross-host placement. Do not move the singleton PG/fixture onto
the host being lost.

## Reversible loss actions

Set PATH and KUBECONFIG to this lab explicitly. Node faults run a supplied
blocking verifier while active and restore on exit, including INT/TERM:

```sh
loss.sh stop      "$state/agent1" "$PEER_IP" VERIFIER ARGS...
loss.sh pause     "$state/agent1" "$PEER_IP" VERIFIER ARGS...
loss.sh partition "$remote_state" "$SERVER_TAILSCALE_IP" VERIFIER ARGS...
role-pause.sh ether035-mh-ether-source-0 VERIFIER ARGS...
role-loss.sh ether035-mh-ether-delivery-0
remote-loss.py --container-id "$CAPTURED_GPU_AGENT_ID" --fault pause -- VERIFIER ARGS...
```

`stop` checks running=false/PID0; `pause` freezes a whole Kubernetes node.
`partition` changes only INPUT/OUTPUT rules inside that node container's private
network namespace. It blocks the specified tailnet peer, never host traffic.
`remote-loss.py` uses an authenticated BB terminal on gpu-workstation, checks
that exact immutable ID's lab label/private namespace/2GiB+1CPU cap, runs the
local verifier during the remote fault, then captures running/unpaused restore
evidence. It has a180s fault bound and aborts/stops that node if host PSI full
avg10 exceeds10. Raw terminal and restore summaries stay in the invoking
thread's storage. Do not infer protocol continuity from successful restoration.
Run per-role pause on the Docker host that owns the selected node container.
Per-role pause freezes the immutable containerd task and its descendants,
without pausing co-located roles. Abrupt role delete uses gracePeriodSeconds0
with a Pod UID precondition; the StatefulSet recreates the pod. Select the actual
active source/encoder/controller from durable session routes immediately before
action; deleting an unused replica proves nothing. These helpers do not claim
fencing, retirement, client continuity or database HA.

## Teardown

First stop owned port-forwards/collectors and drain live sessions. Run
`teardown.sh "$state"` on each host for that host's captured container IDs.
It checks lab labels, removes only those containers, then the captured owned
bridge. Bound data and private credentials are retained for review; dispose of
those exact private state directories only when no longer needed. Never prune
Docker, restart WSL/Docker or remove another lab's resources.

## A second isolated lab

All provisioning/install/import/teardown scripts accept a private `lab.env` in
STATE (or its parent for a per-node state). Defaults retain the original lab.
For example, store the following in a separate private state directory:

```sh
ETHER_LAB_NAME=ether035-lab2
ETHER_LAB_PREFIX=ether035-lab2
ETHER_LAB_NAMESPACE=ether035-lab2
ETHER_LAB_CONTEXT=ether035-lab2
ETHER_LAB_PORT_OFFSET=100
ETHER_LAB_SERVER_MEMORY=4864m
ETHER_LAB_SINGLE_NODE=true
ETHER_LAB_POD_CIDR=10.75.0.0/16
ETHER_LAB_SERVICE_CIDR=10.76.0.0/16
ETHER_LAB_BACKEND=vxlan
```

This profile publishes API16543 and ingress18135, Host `ether035-lab2.test`.
Create only the server with `node.sh`; omit `local-agents.sh`. Capture its
kubeconfig as above using the new API port and context. The single-node install
labels that server for workloads. Two replicas of each role still run, but only
pod/task faults are meaningful; node failure also removes PostgreSQL/ingress.
The enclosing Docker limit bounds all workloads to4.75GiB. A multi-node profile
instead uses `ETHER_LAB_SERVER_MEMORY=1792m` and
`ETHER_LAB_AGENT_MEMORY=1536m` (also4.75GiB total), omitting SINGLE_NODE.
VXLAN is appropriate for a local bridge; the original WireGuard topology remains
required for a second physical host. The optional native-worker relay and
redeploy builder remain specific to the original lab; use import/install with
an existing reviewed image for the second lab.

Use independent DB/issuer/cluster-key generation, fixture resources and bound
state; never copy another lab's secrets, PVCs or identities. Import the runtime
with `import.sh STATE IMAGE`, then `install.sh STATE IMMUTABLE_TAG`. To seed a
fresh lab2 with generated local credentials and the supported frontend APIs:

```sh
KUBECONFIG="$state/kubeconfig" python3 packaging/kubernetes/seed-lab.py \
 --context ether035-lab2 --namespace ether035-lab2 \
 --base http://127.0.0.1:18140 --kubectl "$state/bin/kubectl" \
 --credentials-dir "$state/playback-private" \
 --fixture-quality --enable-mediabrowser
```

The loopback port-forward must target a server pod's32400. `--fixture-quality`
allows the actual generated360p title by lowering profile height/size minima;
it does not change stream metadata. Jellyfin/Emby responses with device-bound
tokens are saved as `jellyfin-auth.json`/`emby-auth.json` beside `local-owner.json`.
Keep those files private. For loss helpers, export `ETHER_LAB_STATE="$state"`
with this lab's KUBECONFIG/PATH; they check the configured context, prefix,
namespace and Docker label before applying a fault. `loss.sh` takes per-node
state directly. Original lab defaults are unchanged.

## Long fixture, PG and orderly updates

The added renderers and PG/roll helpers accept only the original
`ether035-multihost`/`ether035-mh`/`ether035-ha` tuple and the matching
`ether035-lab2` context/prefix/namespace tuple. Load the private `lab.env` through
`lab-config.sh` for shell helpers; standalone renderers use ETHER_LAB_PREFIX
and ETHER_LAB_NAMESPACE. Do not copy secrets or identities between labs.

`long-fixture.py ROOT IMAGE` renders a separate 600s generated source for
900002/rating key1000000000900002. Its Service/ConfigMap, release, Newznab ID
and NNTP article namespace are separate from900001. It retains the original
fixture bytes. Create the rendered `${namespace}-long-source-code` ConfigMap
from source-fixture.py and apply only in the approved lab namespace. Seed via
`seed-lab.py --long-fixture` with that lab's approved namespace/context and
loopback owner origin. This preserves the other known fixture and still
supports `--fixture-quality` and `--enable-mediabrowser`. `--refresh-only`
continues to avoid source configuration writes. Use a separate long reference.
Repeated install preserves an already initialized original source Deployment
and ConfigMap, so a runtime update cannot regenerate its media unexpectedly.
It also omits an existing PG StatefulSet from apply, preventing an emptyDir
template change from replacing a live database.

Lab PG now renders a512MiB memory-backed volume with fsync,
synchronous_commit and full_page_writes OFF. Replacing that pod loses its DB.
Existing disk-backed PG requires explicit quiescence and a private complete
backup before `migrate-pg-tmpfs.sh STATE`: it refuses live role pods or a repeat
migration, retains the old PVC, restores every table/sequence and restores
service routing after verification. Never edit ownership rows to migrate.
An interrupted restore retains the backup/PVC; inspect before restoring service.

Run heavy work through `heavy-io.sh COMMAND ARGS...`; it serializes against
/data/bb/thread-storage/heavy-io.lock, waits for batch-gate.active and reports
queue/run/total seconds. It exits75 if a gate wins the lock race. These helpers
do not create or take over another worker's priority marker.

For an already-built immutable runtime, archive its exact packaging/helm tree
and pass ETHER_LAB_CHART to `heavy-io.sh roll-gated.sh STATE IMAGE_TAG` using
full script paths. The roll preserves lab.env, private-origin postrenderer,
physical-host overlay and role PVCs. It drains and UID-checks replacements,
stops on drain failure, and expects eight role pods. It never force-deletes.
ETHER_LAB_IMAGES_IMPORTED=1 skips an independently verified image import;
otherwise import.sh handles the selected lab. Verify runtime hashes, readiness,
clock health and current/displaced/active claims before releasing playback.
If PG is still disk-backed, the helper first drains all role writers for the
explicit backed-up tmpfs migration; it never resets a live tmpfs DB.

Only offline syntax/render/mock contracts were checked for this integration.
These updated lab2-aware migration/roll paths have not been executed against a
live cluster. Existing lab acceptance belongs to its pinned runtime and does
not validate this new tooling port. Schedule any runtime use separately.

### Additional generated playback modes (review before installation)

`mode-fixture.py` adds only profiles `ramp96`, `ac3-96` and `copy96` to the
existing lab fixture transport. Its manifest binds each exact artifact's file,
size and SHA256 to one namespace, TMDB key, Newznab ID and NNTP article prefix.
The extra media stays file-backed: NNTP reads at most 256 KiB per article, HTTP
streams 64 KiB blocks, and each listener accepts at most four concurrent clients.
The original 180s fixture remains the default. The separate 600s transport is
unchanged. Neither the manifest nor the title seed supplies runtime media proof.

The manifest has `version:1`, the approved lab `namespace`, and exactly three
`profiles`. Each entry has `id`, `tmdb`, `ratingKey`, `file`, `bytes`, `sha256`,
`title`, `release`, `fixtureId` and `articlePrefix`. IDs must be
`<namespace>-<profile>-fixture` and `<namespace>-<profile>-part` respectively;
TMDB IDs are 900003, 900004 and 900005 plus the existing movie rating-key offset.
Raw artifact SHA is an artifact identity. Observe shipping `core.SourceRevision`
after real preparation; never replace it with that SHA. Copy acceptance also
requires the authenticated shipping CopyTimeline/keyframe binding, actual
VideoCopy/AudioCopy/remux admission and a fresh reference. A release label
without a resolution tag claims no larger resolution; normal quality rejection
must remain visible.

`mode-fixture-resources.py` renders a **review-only** resource bundle from the
captured original fixture Deployment, manifest and measured old file size. It
checks namespace/context isolation, reserves 16 MiB beyond the actual media total
within a 128 MiB PVC request, and emits UID/resourceVersion/template-guarded JSON
patches. It never invokes Kubernetes. The bundle creates only a new immutable
code ConfigMap, retained-byte media PVC and bounded upload Pod. It preserves
existing Service/config/role resources. Its rollback pod template uses the
original code ConfigMap and retained media PVC, with the generation init
container removed. Restoring the original generation init would overwrite the
retained fixture and is forbidden.

`seed-mode-titles.py` renders collision-aborting, title-only SQL. It has no
connection/apply option and never writes providers, streams, auth or claims.
Apply it only through the existing lab seed flow after separately reviewed
fixture replacement. Both normal runtime handoffs, quiescent claims and an
explicit fixture window must precede live use of any rendered bundle. Account
for media staging separately from the image-build space allowance and retain
the 40 GB guard; a PVC request does not prove disk headroom. Retain original
fixture hashes/PG identities before and after, and close the UID-bound upload
Pod before handing first preparation/reference/admission to the harness.

Offline contracts (no FFmpeg, providers or database):

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -B -m unittest discover \
  -s packaging/kubernetes/multihost -p test_mode_fixture.py -v
```
