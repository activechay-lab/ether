# Isolated role lab evidence, 2026-11-05

This record is startup and failed playback evidence. It does not establish remote
media delivery, clean packet timing, node-loss recovery or distributed GPU use.
Single-role specialist deployments (server, source, transcode, delivery) are now
enabled on the integration branch; combined and background-only subsets stay refused.

The retained kind cluster is `ether035-ha`, namespace `ether035-ha`. The fresh
`ether035-roles` release uses one dedicated server, source, transcode and delivery
pod and new per-ordinal `/config/ether` PVCs. The existing full-server release,
PostgreSQL, synthetic NNTP/Newznab fixture and old scoped forwards remain intact.
The old transcode node successfully drained before role staging and remains
present with `draining=1`; it was not re-enabled or deleted.

## Candidates and startup

LAB `0dfaa36a61889194908888319e219388cd864692` was deployed under the coordinator's
initial approval. Its image manifest was
`sha256:0376948b68f6bc6f1cc4461e955f17c05cbfda84627bacf0e3a77549984107f6`.
All four pods became Ready; no media requests were sent before the subsequent
auth-ownership review hold. This candidate has startup-only evidence.

The corrected, explicitly approved candidate is
`982a06453878b1576bac4c9d539eb1b68bd3e90d`, fetched from canonical GitHub branch
`bb/lab-only-035-singleton-authfix-thr_fj86rvzs3z`. One niced amd64 repository
Dockerfile build used the pinned FFmpeg bundle and a 4 GiB / 2 CPU builder. Labels
record that exact OCI revision and `ether.lab-only=true`. The builder stopped
after build. Image `ether035-ha:lab-982a06453` was loaded only into the named kind
cluster; its manifest digest is
`sha256:ed61882d3070cc3c751b5eb4aad49d3322c5edf83c0301a51ccc54ffd1e31605`.
Its config digest, independently verified against Kubernetes runtime imageID, is
`sha256:d2bc76f3952b279987fa56a763aebb63df4b0d51caafb76cbbb50517b1ee4287`.

Chart/harness commit `caffb5326` supplied `roles-lab.values.yaml` and the isolated
plain Kubernetes Ingress controller fixture. `canonicalHLSLabOnly=true` remains
explicit. The corrected image replaced source, transcode, delivery and server
sequentially using successful CLI drain followed by atomic pod-UID-preconditioned
deletion. All four became Ready with zero restarts. Their stable node IDs and PVC
UIDs were retained; all four boot IDs changed.

| Role | Pod UID after replacement | Stable node ID | Fresh boot ID |
| --- | --- | --- | --- |
| source | `11ff0ce9-6155-4c6f-8674-6285cbcbcdfa` | `2fafed8783aff03b06528063e2474b36` | `f9a5fb8349bc25bb46ec216d86b549d8` |
| transcode | `4a4fc17a-9040-48ef-a31d-f7b374e732c0` | `75ca4128854983fa096237efbdd06e34` | `96e8297f4ecc52a3548126beba6ff91f` |
| delivery | `6906c5a8-db31-41c3-a4d0-41e9e98dc56f` | `14254d8893907285afbfc24de1ed620b` | `6923f12c342f4b86be09e39db921a4fe` |
| server | `e1fefea3-dd31-48d2-b79e-699e8e5da615` | `bde863fb02c785211132c7df93816cdf` | `c6a76429627a6f3810554e4514341ef5` |

The retained data PVC UIDs are source `b7d363d6-0018-4d0e-b9ed-41f8077612ff`,
transcode `95c80e09-f3c0-420b-89d4-f199eec384e0`, delivery
`acbba998-0ac2-4784-9a58-6a590de5acb6` and server
`afe397e7-0ca2-43b5-b3c0-d73a84db07ad`. None was recreated or migrated.

## First same-origin request: failed before media

At `2026-11-05T19:09:21Z`, session `roles-hls-001` used the ordinary Plex HLS start
route with generated delegated lab authentication through the shared Ingress
host `ether035-roles.test`. Request ID `roles-media-001-982` correlated the
controller's HTTP 500 with the API backend `10.244.2.13:32400`. The server logged
`placement: capacity exhausted`. The source logged `probe failed; using estimated
media info` with `not available yet`.

The fixture was unchanged: a 32,506,600-byte faststart MP4 containing MPEG-4 video
and AAC audio. Source counters increased by one NZB download, one NNTP BODY and
269,329 article payload bytes. This shows source access, not a completed media
pipeline. The exact new encoder target advertised only transcode and had capacity
one. Claims, sessions and history receipts were all zero; fleet quota was one
million. All new members were current and nondraining. The allocation oracle
failed because no durable allocation/route existed. No media segment or packet
continuity evidence was collected, and no loss or native stage followed.

Offline probing of the unchanged fixture confirms the native error:
`transcode: native probe incomplete: video sample entry mp4v`. The runtime worker
used its actual first 262,144 bytes with the honest total size of 32,506,600;
the extracted head includes the complete 93,310-byte moov. Its SHA256 is
`171ecb6fc913ac9121e44e8a633d2b8ee266a25ae09cbed75efa0c320a6fd8b9`.
No source playback retry was needed. Static tracing then shows the source-role `directOnly.Probe` fallback
returns `ErrNotAvailable`. The resulting estimated media information is rejected
by the dedicated encoder's real preflight. Runtime/playback owners are repairing
source-owned bounded probing. The exact encrypted capability HTTP response and
source decision DTO were not logged by this image and are not asserted here.
No metadata was stripped, fixture codec changed, claim released or identity reset
to make the request pass.

Nonsecret snapshots and private diagnostic logs remain under the owned
`/tmp/ether035-tools` lab evidence directory. Credentials, grants and raw private
URLs must not be copied into this record. The strict allocation oracle separately
checks four distinct role identities, boots, the exact image tag and runtime
imageID and the singleton delivery endpoint. It always leaves
`deliveryTrafficVerified=false`; actual request-correlated delivery bytes and
clean packet timing must be proven separately before any node-loss test.
