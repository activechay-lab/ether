# Private boot-bound TLS

Unwired primitives only; no listener, public route, specialist mode, provisioning
service, persistence or enrollment is installed. Credentials are independent of
Ether's shared encryption key and public Plex certificate. Only explicit roots
are accepted. Errors deliberately omit credentials and upstream policy details.

`Identity` is the exact cluster identity, persistent `cluster.Identity.NodeID`,
and the process's existing `cluster.Node.Incarnation` (Postgres generates this
once at construction). Never mint another boot for TLS, reuse a previous boot,
copy node.json, or interpret a display name as an identity. Fields are bounded
ASCII tokens compatible with nodesource and remotetranscode DTOs. The sole URI
SAN is `ether-node://identity/v1/<cluster>/<node>/<boot>`, with no URI aliases,
queries, fragments, escapes or extra identities.

An authorized issuer receives an explicit CA certificate/Ed25519 signer, checked
Ed25519 CSR, trusted identity, and 1–8 exact DNS/IP SANs. `Issue` ignores CSR
requested extensions and bounds validity to 24 hours and the CA's validity.
Hostnames are lowercase ASCII DNS without wildcards; IP SANs are supported.
Enrollment must authorize all identity/host values independently of the CSR.
`NewSnapshot` parses/copies leaf/key PEM and explicit CA PEM; no internal mutable
TLS config, pool, certificate or private key slice is returned to callers.
Configs returned by builders are individually owned by their recipient.

`NewGuard` requires a live `Policy` and clock. Policy must check complete current
cluster/node/boot membership and revocation; nil/unavailable policy fails closed.
Policy can inspect the copied certificate serial for credential revocation.
Callbacks run without locks and must be concurrency safe. Caller-controlled
placement claims never establish membership. `ClientTLS(expected, hostname)`
checks BOTH a complete expected peer identity and ordinary DNS/IP hostname,
chain, EKU and validity. `ServerTLS` requires verified client certificates and
checks cluster membership; the intended caller and operation are additionally
checked by consumer authorization. Minimum TLS is 1.2; resumption is disabled.

`Replace` atomically publishes a snapshot for the SAME local cluster/node/boot.
New server handshakes select it automatically. For clients, rebuild the config
and HTTP transport after root/client credential rotation and close old idle
connections. This deliberate requirement retains ordinary Go TLS verification;
there is no InsecureSkipVerify or dynamic trust bypass. Use overlapping explicit
root bundles while rotating. Failed snapshot validation leaves the old one intact.
Request checks use the latest snapshot, so removed roots, expiry, old boots and
revocations fail even on established connections. Checks are point-in-time;
policy updates racing admission still require consumer atomic authority checks.

Integration:

- remotetranscode: `Security.Authenticate = guard.RemoteAuthenticate` returns the
  certificate **Boot**, since Controller is boot-scoped in that DTO. Policy still
  receives the complete identity and must bind the current controller membership;
  `Security.Authorize` must independently check placement/generation/source rights
  for every operation. Supply `ClientTLS` to its private HTTP client.
- nodesource: `Options.Peer = guard.SourcePeer` returns stable NodeID and Boot,
  checking current roots/expiry/policy on every call (including reused TLS).
  This leaf-only API supports direct trusted-CA issuance; for intermediate chains
  compose `guard.Request` in a private request wrapper instead. Keep its existing
  verified-chain handler checks. Supply `ClientTLS` and `BindServer` to the client.
- Handshake callbacks alone cannot revoke active connections. Private runtime
  clients MUST call `RecheckServer` per operation with established connection
  state (including reused connections). Existing consumer clients do not expose
  that hook; their runtime transport integration remains followup. Server handlers
  use `Request` per operation. Long source copies additionally require live policy
  through `Authority.Verify` at their existing cadence; the leaf mapper runs at
  request admission only. Certificate trust is never placement/source authority.

Provisioning, issuer custody, root bundle installation/bootstrap/adoption,
authorized CSR enrollment and rotation scheduling remain host responsibilities.
There is no automatic CA generation through the shared database. Existing
clusterconfig.Join consumes single-use tokens before writing local files. This
package does not reuse those tokens or solve idempotent Kubernetes enrollment.
No CA key belongs in notifications, logs or shared encryption material. Deployment
must supply clock synchronization and conservative policy expiration bounds.
