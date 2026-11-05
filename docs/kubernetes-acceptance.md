# Reproduce the generated-media Kubernetes lab

This harness uses synthetic FFmpeg video/audio and lab-only credentials. It
starts the shipping Ether executable, PostgreSQL, a Newznab indexer fixture and
a real multipart yEnc NNTP service. Do not run it against production namespaces,
provider accounts or a claimed Plex server. Use the named `ether035-ha` kind
cluster and namespace. Fixture health is infrastructure readiness only.

Current staging evidence: runtime `a73b5c7a7` starts full+transcode pools; generated
NNTP media plays through the ordinary Plex route but that candidate encodes
locally. Its Plex adapter bypasses the distributed broker. The observed zero
placement rows make this **local pipeline evidence only**. A later reviewed
candidate must enable Plex distributed composition before this can pass remote
encoding or node-loss acceptance. Never strip genuine stream metadata to force a
plan through an unsupported gate. Source/server-only/delivery roles remain
runtime-gated until their actual graphs are implemented and tested.

## Start the isolated fixtures

Use `packaging/kubernetes/kind.yaml` for one control plane and two workers.
Build the exact reviewed runtime candidate with the repository Dockerfile,
checksum-verified `scripts/fetch-ffmpeg.sh linux-amd64` output, explicit amd64
platform and an OCI revision label. Load only that image into `ether035-ha`.
`source-fixture.yaml` currently names `ether035-ha:a73b5c7a7`; update its generator
image to your reviewed candidate. Do not use this staging tag as a release.

Prepare the lab PostgreSQL and Secret refs as described in `kubernetes.md`,
with newly generated test-only DB password, 32-byte raw encryption key and an
independent Ed25519 issuer. PostgreSQL must be a named lab fixture pod
`ether035-ha-pg-0` labeled `app=ether035-ha-pg`. A singleton is sufficient for
initial pipeline experiments and is not database HA. Keep database authority on
a surviving node for encoder-loss tests; a worker loss that also kills the only
database cannot prove database availability. Local-path PVCs do not move data
between nodes automatically.

Create the synthetic source:

```sh
kubectl -n ether035-ha create configmap ether035-ha-source-code \
  --from-file=packaging/kubernetes/source-fixture.py
kubectl -n ether035-ha apply -f packaging/kubernetes/source-fixture.yaml
kubectl -n ether035-ha rollout status deployment/ether035-ha-source
```

The init container generates 180 seconds of MPEG4 video and sine-wave AAC audio
into an emptyDir. The Python fixture publishes Newznab caps/search/NZB on 8080
and NNTP article bodies on 1190. It does **not** serve media over HTTP. Article
bodies use real multipart yEnc with per-part CRC32 and the final whole-file CRC.
The fixture is pinned to the control plane to survive worker process-loss tests.
`GET /stats` reports NZB grabs, NNTP body requests and bytes without logging API
keys or query strings. No upstream provider is contacted.

## Seed lab identity, metadata and sources

Port-forward a healthy full node's public listener to loopback:

```sh
kubectl -n ether035-ha port-forward pod/ether035-ether-full-1 18035:32400 \
  --address 127.0.0.1
```

In a second shell with the lab kubeconfig selected:

```sh
python3 packaging/kubernetes/seed-lab.py --base http://127.0.0.1:18035 \
  --credentials-dir /tmp/ether035-lab-private
```

The seed script requires the named kind context, namespace and PostgreSQL fixture
label. It creates a local owner through the shipping setup API, then configures
only the fixture NNTP provider and indexer through the owner config API. It
refuses claimed servers and nonfixture provider/indexer replacement. A detailed
synthetic catalog row is seeded directly in the isolated PG database because
there is no shipping title-create API; timestamps suppress upstream metadata
refresh. Rating key `1000000000900001` is `MovieKey(900001)`, not a raw TMDB ID.

The script issues a random short-lived local delegation by inserting only its
SHA256 into the shipping `auth_delegations` table, bound to the active lab viewer
with a 15-minute expiry. Its `ether-cast-` credential is accepted by Plex auth and
has no upstream account rights. Admin identity grants are **not** raw Plex
credentials. Private files contain the owner password, admin grant, playback
credential and request headers; keep them out of values, logs and artifacts.
Rerunning the seed refreshes the lab delegation without creating a Plex claim.

## Measure actual media and actual placement

Use the real generated metadata and ordinary Plex transcode request:

```text
/library/metadata/1000000000900001
/video/:/transcode/universal/start.m3u8?path=%2Flibrary%2Fmetadata%2F1000000000900001&session=ether035-ha-generated-001&protocol=hls&directPlay=0&directStream=0&videoCodec=h264&audioCodec=aac&maxVideoBitrate=1000&videoResolution=640x360
```

Send the saved playback headers, then follow the returned playlists/segment URLs.
Store grant-bearing URLs only in a private probe config. Do not print the
playlist or token. The MPEG4 input requires a genuine H264 transcode; fetching a
source file or a direct-play response cannot substitute for that work.

`media-probe.py` accepts this private JSON config (replace the manifest URL with
the actual request/returned URL; header file is generated by the seed script):

```json
{
  "manifestURL": "http://127.0.0.1:18035/ACTUAL-MANIFEST-URL",
  "allowedOrigins": ["http://127.0.0.1:18035"],
  "headersFile": "/tmp/ether035-lab-private/playback-headers.json",
  "durationSeconds": 90,
  "maximumGapSeconds": 5,
  "minimumSegments": 10,
  "requestTimeoutSeconds": 5
}
```

```sh
python3 packaging/kubernetes/media-probe.py --config /tmp/probe.private.json \
  --output /tmp/ether035-media-evidence
```

The collector fetches real clear MPEG-TS HLS, paces consumption at media duration,
records bytes, SHA256, request latency, media sequence and actual video PES
presentation timestamps, and rejects sequence gaps, timestamp discontinuities,
HTTP errors or progress gaps beyond budget. It stores numbered media segments,
not raw grant URLs. It rejects unsupported encrypted/fMP4 formats rather than
pretending to validate them. Validate decoding of the collected generated
segments with the packaged FFmpeg/ffprobe as a separate step. Collector validation
was exercised with real generated FFmpeg TS and a deliberately substituted later
segment: clean media passed and the actual PTS gap failed.

Its summary always says `placementAndFencingVerified: false`: the collector does
not observe execution ownership. To prove the distributed pipeline, capture
independent PG evidence for the exact session/boot/generation in
`placement_sessions`, claims and route tables; map the source owner to the
source node and the encoder target to a different transcode-only node; capture
actual NNTP reads and encoder process/output evidence; and fetch through a
surviving different public node to prove delivery forwarding. A local FFmpeg
start and zero placement rows is local fallback, even if all media decodes.
Never publish raw source grants, bearer tokens, Secret values or full FFmpeg
command lines in these evidence artifacts.

## Controlled loss

Keep source, database, encoder and public delivery identities explicitly mapped
to failure domains. Use `hard-loss.sh` only with a verifier that runs the media
collector across the loss and checks the exact durable placement/generation and
retirement authority. The helper captures a worker container ID together with
its kind authorization labels, operates only on that ID, checks stopped/PID0,
and restores it on exit. Parent authority evidence must establish whether the
exact encoder/source process died; silence or a failed probe cannot retire it.

Measure timestamps before/during/after loss, playback progress, maximum delivery
gap, errors, range/seek behavior, duplicate encoding and durable capacity held
until proof. No successful single-node HLS read or stopped Docker container alone
constitutes seamless node-loss acceptance. Preserve unresolved claims if proof
is missing. Repeat separately for graceful drain, abrupt encoder loss, source
loss and public ingress/controller loss with surviving authority/capacity. Stop
owned port-forwards and verifier children afterward; preserve the explicitly
requested kind lab.
