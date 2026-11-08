# Kubernetes chart values

Every supported global value is listed below. Kubernetes-native objects accept their normal fields; the chart schema rejects unknown chart keys. Do not put credential bytes into any value. Defaults are from `packaging/helm/ether/values.yaml`.

| Value | Default | Purpose |
| --- | --- | --- |
| `image.repository` | `"ghcr.io/braedonsaunders/ether"` | Container registry/repository. |
| `image.tag` | `"0.3.5"` | Explicit runtime version, ignored when digest is set. |
| `image.digest` | `""` | Optional immutable sha256 digest. |
| `image.pullPolicy` | `"IfNotPresent"` | Always, IfNotPresent or Never. |
| `imagePullSecrets` | `[]` | Existing registry Secret references. |
| `clusterDomain` | `"cluster.local"` | Cluster DNS suffix. |
| `runtime.ledgerCapacity` | `1000000` | Fleet-wide persisted quota on live (unretired) placement sessions, 4096–10000000; all replicas must agree. Retired history never counts; the leader compacts it after 24 hours. |
| `runtime.capacity` | `1` | Default runtime capacity, 1–4096; pool capacity overrides it. |
| `runtime.clockBoundKnown` | `false` | Must be true only after verifying the clock error bound. |
| `runtime.clockSkewMillis` | `1000` | Verified maximum clock error, 0–60000 ms. |
| `secrets.database.existingSecret` | `""` | Existing Secret name (required). |
| `secrets.database.key` | `"database-url"` | Key inside the existing Secret; only this file is mounted. |
| `secrets.cluster.existingSecret` | `""` | Existing Secret name (required). |
| `secrets.cluster.key` | `"cluster-key"` | Key inside the existing Secret; only this file is mounted. |
| `secrets.authority.existingSecret` | `""` | Existing Secret name (required). |
| `secrets.authority.certKey` | `"ca.crt"` | Key inside the existing Secret; only this file is mounted. |
| `secrets.authority.privateKey` | `"ca.key"` | Key inside the existing Secret; only this file is mounted. |
| `secrets.authority.rootsKey` | `"roots.pem"` | Key inside the existing Secret; only this file is mounted. |
| `data.size` | `"1Gi"` | Identity/config PVC request per pod. |
| `data.storageClass` | `""` | StorageClass; empty omits field and uses cluster default. |
| `data.accessModes` | `["ReadWriteOnce"]` | Config PVC access modes. Unique PVC per ordinal even for RWX. |
| `cache.type` | `"emptyDir"` | emptyDir or persistent. |
| `cache.size` | `"20Gi"` | emptyDir limit or persistent cache PVC request. |
| `cache.storageClass` | `""` | StorageClass for persistent cache. |
| `scratch.sizeLimit` | `"10Gi"` | Scratch emptyDir limit. |
| `resources` | `{"requests":{"cpu":"100m","memory":"256Mi"},"limits":{"memory":"1Gi"}}` | Kubernetes requests/limits, including optional ephemeral-storage. |
| `nodeSelector` | `{}` | Kubernetes node labels to match. |
| `tolerations` | `[]` | Kubernetes tolerations. |
| `affinity` | `{}` | Kubernetes affinity/anti-affinity. |
| `topologySpreadConstraints` | `[]` | Native Kubernetes constraints; include your release/role label selector. |
| `runtimeClassName` | `""` | Existing RuntimeClass, omitted when empty. |
| `podAnnotations` | `{}` | Additional annotations; avoid reserved checksum/deployment. |
| `podSecurityContext` | `{"runAsNonRoot":true,"runAsUser":999,"runAsGroup":999,"fsGroup":999,"fsGroupChangePolicy":"OnRootMismatch","seccompProfile":{"type":"RuntimeDefault"}}` | Native pod security context; volume driver must honor fsGroup. |
| `securityContext` | `{"allowPrivilegeEscalation":false,"readOnlyRootFilesystem":true,"capabilities":{"drop":["ALL"]}}` | Native container security context. |
| `terminationGracePeriodSeconds` | `180` | Kubernetes termination grace; does not prove work retirement. |
| `probes.startup.periodSeconds` | `5` | Positive integer kubelet probe setting; routes/port are fixed by the runtime contract. |
| `probes.startup.failureThreshold` | `120` | Positive integer kubelet probe setting; routes/port are fixed by the runtime contract. |
| `probes.startup.timeoutSeconds` | `2` | Positive integer kubelet probe setting; routes/port are fixed by the runtime contract. |
| `probes.readiness.periodSeconds` | `3` | Positive integer kubelet probe setting; routes/port are fixed by the runtime contract. |
| `probes.readiness.failureThreshold` | `1` | Positive integer kubelet probe setting; routes/port are fixed by the runtime contract. |
| `probes.readiness.timeoutSeconds` | `2` | Positive integer kubelet probe setting; routes/port are fixed by the runtime contract. |
| `probes.liveness.periodSeconds` | `10` | Positive integer kubelet probe setting; routes/port are fixed by the runtime contract. |
| `probes.liveness.failureThreshold` | `3` | Positive integer kubelet probe setting; routes/port are fixed by the runtime contract. |
| `probes.liveness.timeoutSeconds` | `2` | Positive integer kubelet probe setting; routes/port are fixed by the runtime contract. |
| `disruptionBudget.enabled` | `true` | Create one PDB per active role. |
| `disruptionBudget.maxUnavailable` | `1` | Integer or percentage unavailable during voluntary evictions. |
| `service.type` | `"ClusterIP"` | ClusterIP, LoadBalancer or NodePort for full-only public pools; mixed server/delivery pools require ClusterIP behind Ingress. |
| `service.port` | `32400` | Public service port; target always 32400. |
| `service.annotations` | `{}` | Controller/provider annotations. |
| `ingress.canonicalHLSLabOnly` | `false` | Temporary explicit acknowledgement for isolated canonical-HLS mixed-role staging. Required when server/delivery pools are enabled. Writer-derived URL/auth fixtures, real same-origin controller and protocol capability acceptance remain required; this is not final mixed-client parity. |
| `ingress.enabled` | `false` | Expose one shared public origin; required for server/delivery pools, with fixed API/media Prefix routing. |
| `ingress.className` | `""` | Ingress controller class. |
| `ingress.annotations` | `{}` | Controller-specific annotations. |
| `ingress.hosts` | `[]` | DNS hostname list, required when ingress enabled. |
| `ingress.tls` | `[]` | Native Ingress TLS entries referring to existing Secrets. |
| `networkPolicy.enabled` | `false` | Enable ingress and egress isolation. |
| `networkPolicy.publicIngress` | `[]` | Native NetworkPolicyPeer list for public clients/controller. |
| `networkPolicy.egress` | `[]` | Additional native egress rules for DB/providers/APIs. |

Each `pools.full`, `pools.server`, `pools.source`, and `pools.transcode` / `pools.delivery` object supports:

| Pool value | Default | Purpose |
| --- | --- | --- |
| `replicas` | full 1; others 0 | Desired ordinals, 0–1000. Drain before reducing. |
| `bootstrap` | full true; others false | Seed-or-verify database; true permitted only for full/server. |
| `capacity` | runtime.capacity | Runtime capacity, 1–4096. |
| `resourceScope` | runtime node ID | Optional physical resource capacity scope. Use consistently for workers sharing a device. |
| `resources` | global, except full has 250m/512Mi requests and 2Gi memory limit | Complete requests/limits override. |
| `nodeSelector`, `tolerations`, `affinity`, `topologySpreadConstraints` | global | Nonempty pool setting overrides the corresponding global object/list. |
| `runtimeClassName` | global | Nonempty pool class overrides global class. |
| `podAnnotations` | global | Merges keys over global annotations. |
| `data`, `cache`, `scratch`, `disruptionBudget` | global | Partial object override using the same supported subkeys. |

Role pool names and container public/private/probe ports are fixed. Database URLs,
encryption keys and issuer keys are supplied only through existing Secret refs.
No arbitrary environment/command/volume injection or implicit shared PVC is
supported. To clear a global nonempty placement object/list, set the global
value empty and put explicit settings on the other pools. Resource objects are
complete pool overrides rather than merged limit maps.


## Four specialist pools

The following is an isolated mixed-role starting point. Replace Secret names,
hostname, IngressClass and the clock assertion with verified site values. The
lab acknowledgement is required by the current chart. Supply placement rules
that separate each role's replicas across actual failure domains.

```yaml
runtime:
  clockBoundKnown: true
  clockSkewMillis: 1000
secrets:
  database: {existingSecret: ether-db}
  cluster: {existingSecret: ether-cluster}
  authority: {existingSecret: ether-authority}
pools:
  full: {replicas: 0}
  server: {replicas: 2, bootstrap: true}
  source: {replicas: 2}
  transcode:
    replicas: 2
    resources:
      requests: {cpu: "1", memory: 512Mi}
      limits: {cpu: "2", memory: 2Gi}
  delivery: {replicas: 2}
ingress:
  enabled: true
  canonicalHLSLabOnly: true
  className: YOUR-INGRESS-CLASS
  hosts: [ether.example.test]
```

Only server/full may bootstrap shared configuration. After initial creation,
`bootstrap: false` requires existing settings. Empty runtime role arrays select
the full preset; the chart maps each specialist pool to its fixed responsibility.
There is no runtime role mutation or automatic safe scale-down. Use explicit
drain and sequential UID-safe replacement for rollouts; keep identity PVCs.

Shared playback settings are not Helm values. Eligible copied-audio FastStart
is authored once by the server and transported as an immutable ramp/timeline;
the specialist no longer forces FastStart off. Automatic ramp authoring skips
re-encoded audio, whose admitted recovery profile is steady. Private specialists
still disable adaptive ladders (including under-copy), the spike governor and
RTX Video enhancement. Remux recovery requires the bounded authenticated source
timeline, not a Helm setting. See
[automatic recovery admission](kubernetes.md#automatic-recovery-admission).

Fresh-boot `/readyz` waits for an owned heartbeat publication of that boot's
usable protocol capability. Probe timing values cannot replace this receipt or
relax peer membership/fencing checks. Successful publication is readiness
proof; real playback/control/retirement acceptance remains separate.

For Linux NVIDIA pods, overlay `packaging/kubernetes/nvidia.values.yaml`, install
the external driver/toolkit/device plugin, label nodes `ether.plex/gpu=nvidia`
and request `nvidia.com/gpu: 1`. Pool `resources` completely replaces global
resources, so include every intended request/limit in that override. Device
scheduling alone does not prove hardware encode support.

Native Windows NVENC specialists are external runtime nodes, not an additional
Helm pool or a Windows pod generated by this chart. Join them with the shared
PostgreSQL/trust configuration and reachable authenticated private transport.
Keep CPU transcode replicas for fallback. Eligible workers are ranked by
exact-request verified hardware support, spare capacity, source locality and
stable identity. Use `pools.transcode.resourceScope` consistently for workers
sharing one physical resource; distinct independent GPUs should not share a
scope. Never put native credentials or private endpoint tokens in Helm values.

Host NTP is mandatory. `clockSkewMillis` is a verified pairwise maximum, not a
request to synchronize clocks. The continuous guard compares each node with the
DB clock and fails authority closed on bad/stale samples; membership timestamps
also use the DB clock. Do not widen this bound to hide an unhealthy clock.
See [clock enforcement and recovery limits](kubernetes.md#continuous-clock-enforcement)
and [the measured acceptance matrix](kubernetes-acceptance.md#release-loss-matrix-and-verdicts).
