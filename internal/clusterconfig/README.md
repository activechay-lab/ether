# Deployment enrollment and local drain control

Standalone SQLite and historical full-server startup are unchanged when
`ETHER_DEPLOYMENT_FILE` is absent. Deployment mode uses PostgreSQL authority,
including with an external database. A bundled singleton PostgreSQL is NOT DB HA.

`ETHER_DEPLOYMENT_FILE` names a mounted node-local JSON document (not a shared
config section). Initial supported runtime sets are full-server (empty roles or
all five roles) and transcode-only, subject to runtime composition acceptance.
Other role sets remain rejected until their actual service graph is integrated.

Example document (all Secret values are separate files):

```json
{
  "bootstrap": true,
  "roles": ["transcode"],
  "internalAddress": "https://ether-transcode-0.ether-private:32401",
  "privateListen": ":32401",
  "probeListen": ":32402",
  "databaseURLFile": "/run/ether-secrets/database-url",
  "clusterKeyFile": "/run/ether-secrets/cluster-key",
  "caFile": "/run/ether-issuer/ca.crt",
  "caKeyFile": "/run/ether-issuer/ca.key",
  "rootsFile": "/run/ether-issuer/roots.pem",
  "capacity": 2,
  "clockBoundKnown": true,
  "clockSkewMillis": 1000,
  "tempDir": "/scratch/transcode",
  "cacheDir": "/scratch/cache",
  "drainSeconds": 120
}
```

Private and probe ports default to 32401/32402. `capacity` (1..4096) is explicit;
clock bound must be explicitly known and skew 0..60000ms. Deployment operators
must maintain that pairwise bound between source, database and encoder clocks.
`resourceScope` optionally names a shared physical encoder budget; otherwise the
persistent node ID supplies the scope. Old boot claims retain capacity on that
scope until authenticated retirement proof. Capacity is admission accounting,
not proof that detected hardware can encode a requested format.

Startup holds the data-directory lock, opens/migrates PostgreSQL, takes a shared
provisioning lock, then seeds or verifies shared settings. `bootstrap:false`
requires existing shared settings. Multiple bootstrap pods with the SAME
provisioned encryption key serialize and adopt the first committed identity.
An existing cluster with a different key fails closed. Cluster key is exactly
32 RAW bytes, not base64 text (Kubernetes Secret data is already decoded on disk).
Never put DB passwords, encryption keys, CA keys or join tokens in Helm values,
command arguments or logs. File paths may be stored in deployment configuration.
The deployment Secret database URL takes precedence over legacy URL environment
overrides in deployment mode.

Every replica needs its own retained PVC/data directory. It generates and retains
its own `node.json`; NEVER distribute or clone that file. Repeated startup with
matching local key/config is idempotent, including after interruption between
local writes or after shared seeding. Mismatched local keys and existing config
without its key require explicit offline recovery; no automatic overwrite or
adoption occurs. An existing configured standalone server requires explicit
`ether cluster init` migration. Trusted provisioning does not consume join tokens:
it can recover a blank replica after a legacy token was consumed before its local
files were installed. Never reuse a single-use token across replicas.

The cluster encryption key and database credential already grant access to the
trusted cluster state. Specialists are NOT a sandbox for untrusted machines.
Issuer custody follows that trust model: a separately generated Ed25519 CA cert
and PKCS8 private key live in a separately administered Secret, restricted to
trusted Ether service accounts. Never derive signing keys from encryption keys.
The issuer cert is directly trusted (no intermediate chain). Each process mints
an ephemeral Ed25519 leaf for its EXISTING membership Incarnation and configured
DNS/IP host, valid twelve hours, refreshed hourly. No extra TLS boot ID is minted.

For CA rotation first distribute an overlapping old+new explicit `roots.pem`,
then swap issuer key/cert, then remove the old root after all members have rotated.
Secret contents are reopened and checked on rotation; node policy and identities
are immutable until restart. Invalid rotation fails closed and does not publish
new capabilities. Encryption key rotation is separate, requires coordinated
credential resealing/restart, and never happens by replacing local settings from
arbitrary mounted bytes. DB password rotation requires application restart.

Probe listener contains only GET `/livez`, `/startupz`, `/readyz`, `/admissionz`.
Liveness has no DB dependency. Startup means real compatible services started;
readiness describes existing-work service availability. Admission additionally
rejects a draining node. Private headless DNS may publish not-ready addresses;
placement still requires verified current membership, exact boot, role, protocol
and admission. Probes never carry media, provider credentials or drain controls.

`ether cluster drain --data-dir /config --wait 120s` (`--timeout` alias) is local
control authenticated by the data-directory owner's filesystem permissions.
It atomically writes a random operation and exact current boot to `drain.request`,
then waits for matching `drain.status` with proven zero pending obligations.
Files must be regular 0600 files; symlinks and group/world-accessible files are
rejected. A stale PVC request/ack cannot drain or acknowledge a new boot.
Timeout returns nonzero, retaining all unresolved ownership/capacity. No public
HTTP drain route exists. Use explicit drain-before-delete and StatefulSet
OnDelete for planned upgrades; preStop is only a bounded fallback, not a promise
of seamless replacement. SIGTERM stops new admission and preserves private
existing-media handlers, membership and lease reconciliation during drain.
Source cleanup runs separately from heartbeat/expiry and may block in backend
Close: a deadline/network silence never proves remote encoder or source retirement.

If SIGTERM reaches `drainSeconds` with unresolved source/encoder obligations,
Ether reports the unresolved drain and KEEPS membership, private handlers and
cleanup running until proof arrives or an external supervisor terminates the
process. Arbitrary backend Close is not cancellable. CLI drain times out nonzero
without stopping the runtime. Kubernetes terminationGracePeriod may ultimately
kill the process; that does not retire its durable reservations. Planned upgrades
must require a successful explicit drain before deletion. Set the external grace
period above the intended drain window, allowing control and shutdown overhead.

The supported deployment topology uses full servers as frontend/source/delivery
coordinators and transcode-only nodes as remote execution targets. Full servers
keep their historical local encoder but are EXCLUDED from private transcode
placement: local legacy sessions do not yet participate in the durable placement
budget, so admitting private encodes there could double-book hardware. This does
not disable their existing local capabilities. Transcode-only nodes use a
separate fixed-plan encoder manager with adaptive ladders/governor/enhancement and
automatic source mutation disabled. Unified local+private physical accounting is
required before full servers can become remote encoding targets.

`ledgerCapacity` is distinct from concurrent encoder `capacity` and boot-local
history retention. It defaults to 1000000, accepts 4096..10000000, and must cover
concurrent/history capacity. Every deployment replica must use the SAME value.
Placement migration 2 stores that fleet ceiling. Initial startup seeds or verifies
it under the serialized placement lock. Every new-generation Reserve checks policy
in the SAME transaction as allocation; conflicting coordinator settings fail
closed. Existing assignment replay/renewal/retirement remain available on a stale
coordinator so correcting policy never destroys ownership. Drain old pre-policy
candidate binaries before introducing this policy; they predate this enforcement.

Changing JSON alone cannot silently raise the ceiling. The trusted operator runs
`ether cluster ledger-quota --data-dir /config --expect 1000000 --capacity 2000000`,
then rolls matching `ledgerCapacity:2000000` settings across the fleet. This is an
explicit monotonic compare-and-swap, never a decrease or table cleanup. Stale
coordinators stop NEW allocations until their configured ceiling matches the new
policy. `/admissionz` includes a `ledger` object with capacity/used/retired/exhausted/
matches; exhaustion returns 503 for new admissions and logs the quota transition,
while `/readyz` and existing work remain available. A larger ceiling is bounded
operational mitigation, not safe history compaction or unlimited admission. Retired
generation floors remain permanent; never delete them to manufacture capacity.
