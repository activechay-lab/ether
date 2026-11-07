# Reproduce the generated-media Kubernetes lab

This harness uses synthetic FFmpeg video/audio and lab-only credentials. It
starts the shipping Ether executable, PostgreSQL, a Newznab indexer fixture and
a real multipart yEnc NNTP service. Do not run it against production namespaces,
provider accounts or a claimed Plex server. Use the named `ether035-ha` kind
cluster and namespace. Fixture health is infrastructure readiness only.

The current integration implements the distributed specialist graphs. Historical
`a73b5c7a7` results showed local encoding and zero placement rows; those results
are baseline diagnostics, not evidence for current remote execution or recovery.
Pin the exact integration SHA/image for every new run. Never strip genuine
stream metadata to force a plan through an unsupported gate. The chart's mixed
public-role lab acknowledgement remains required.

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

Before another mixed-role media run, verify the controller's empty-backend
behavior separately: with an empty disposable media Service and a ready
disposable API backend under one test origin, both canonical media prefixes
must return 503 without reaching API, while browse/control paths still reach
API. The Traefik lab fixture enables `allowemptyservices` for this invariant.
Capture correlated controller/backend logs and exact resource UIDs; remove only
the disposable objects with UID preconditions afterward. Do not remove real
delivery endpoints or create a playback session for this routing check. This
check establishes routing behavior, not media availability or a readiness cause.

Use the real generated metadata and ordinary Plex transcode request:

```text
/library/metadata/1000000000900001
/video/:/transcode/universal/start.m3u8?path=%2Flibrary%2Fmetadata%2F1000000000900001&session=ether035-ha-generated-001&protocol=hls&directPlay=0&directStream=0&videoCodec=h264&audioCodec=aac&maxVideoBitrate=1000&videoResolution=640x360
```

Send the saved playback headers, then follow the returned playlists/segment URLs.
Store grant-bearing URLs only in a private probe config. Do not print the
playlist or token. The MPEG4 input requires a genuine H264 transcode; fetching a
source file or a direct-play response cannot substitute for that work.

For a fresh reviewed Plex attempt, use `plex-baseline.py` to own the complete
Start, authored HLS collection and normal Stop in one synchronous process. The
earlier separate collector workflow allowed setup after Start and a 161-second
gap before the first variant request. Its failed request proves neither media
delivery nor an expiry cause. Prepare collectors and review the private request
config before another attempt; never extend grants or idle policy to accommodate
collector setup.

The private config contains `session`, `requestID`, exact `startURL`/`stopURL`,
original `headers`, absolute pinned `ffmpeg`/`ffprobe` paths and their SHA256
values in `ffmpegSHA256`/`ffprobeSHA256`. Keep the real original request/plan,
fixture and quality unchanged. Optional bounds are `durationSeconds` (5–30,
default30), `minimumSegments` (1–8, default5), `requestTimeoutSeconds` (1–10,
default5) and `maximumFirstGETDelaySeconds` (0.05–1, default1). The config must be
mode0600. Raw manifests, arguments, bodies and observer data remain private.

Exactly one `samplers` entry declares `name`, a Python `argv`, `scriptSHA256`
and `inputs` (absolute `path`/`sha256` pairs). Use the reviewed
`baseline-observer.py` with `--kubectl`, `--expected`, `--session`, `--request-id`,
`--ready-file {readyFile}` and `--output-dir {outputDir}`. Its expectation file
pins the four roles' node/boot/Pod UID/container/image tuples from the strict
allocation oracle, the held server's exact instance, and fixture/Ingress Pod
UIDs. The observer performs read-only checks, rejects an already allocated
session, verifies singleton API/media endpoints, captures source fixture counters
and observes encoder processes and durable placement. It emits readiness only
after initial checks. No source, provider, database policy or runtime control is
mutated. Its exact command, inputs and hashes need review with the attempt.

```sh
# Both commands below perform ZERO HTTP requests and start ZERO children.
python3 packaging/kubernetes/plex-baseline.py --config /tmp/attempt.private.json
python3 packaging/kubernetes/plex-baseline.py --config /tmp/attempt.private.json \
  --dry-parse-master /tmp/previous-master.private

# Separate authorization is required for one fresh session. Output must be new.
nice -n 10 python3 packaging/kubernetes/plex-baseline.py \
  --config /tmp/attempt.private.json --output /tmp/attempt-private-output --run
```

The live group starts the bounded observer and waits for readiness BEFORE its
only Start. It parses the returned master and immediately fetches the authored
variant without a tool/message roundtrip. Monotonic timestamps bracket the end
of the Start body read and the first media GET; a delay above the configured
bound aborts before sending that GET. It preserves authored queries, enforces
the same loopback transport origin/approved lab Host, refuses redirects and
stops on the first HTTP, protocol, packet or decoder failure. It collects at
most30 seconds of media, with a50-second collection wall bound; individual
requests, analysis children, observer setup and cleanup have separate finite
bounds. Every collected packet's PTS/DTS is retained and all audio/video tracks'
DTS must strictly increase across segments. Strict decoder exit is recorded
separately and does not prove decoded completeness.

Normal Stop runs in `finally`, even after an unknown Start outcome. The observer
then finishes its bounded read-only children and is reaped; a forced observer
cleanup or surviving process group fails the harness. Stop HTTP200 never sets
`retirementVerified`: independently check the actual source-preparation and
encoder/input/source retirement receipts before releasing any responsibility.
Likewise, collection does not prove placement, fencing, node-loss continuity or
native GPU execution. The offline check covers orchestration and failure paths
with stubs, without contacting the lab or claiming actual media acceptance.

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
  "requestTimeoutSeconds": 5,
  "packetImage": "ether035-ha:YOUR-REVIEWED-CANDIDATE"
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
pretending to validate them. When `packetImage` is supplied, the collector also runs `packet-proof.py` using
the packaged ffprobe in an owned, bounded, network-disabled container. This
demuxes actual packets and fails duplicate/regressing audio or video DTS. The
container's exact ID is captured and cleaned up even on a timeout. Without that
field, `packetTimingVerified` stays false. PTS-only progress is insufficient:
the initial local baseline delivered media but had duplicate/regressing DTS at
an encoder run transition, which this stricter oracle rejects. Independently
generated clean FFmpeg HLS passed the packet oracle. Validate complete decoding
of the collected segments with error-exit behavior as an additional gate. Collector validation
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

## MediaBrowser delivery aliases

The chart routes `/jellyfin/sessions/media` and `/emby/sessions/media` to the
media Service. Admission, `/Videos/...` and lazy/control routes remain on API.
Returned grant-bearing representation URLs keep the same origin and prefix;
no Ingress path rewrite is required. GET/HEAD and MediaBrowser OPTIONS preflight
requests resolve the admitted execution and replay its original mounted
frontend through an available server. Prefer the current coordinator while it
is published so complete reads populate its recovery store; another server can
serve healthy media without waiting for election. Original query and bounded
account/device headers are preserved, and the broker rechecks the path grant, protocol, active viewer and current policy.

`TestMediaBrowserDeliveryReplaysOriginalFrontendAndFencesLoss` compares actual
HTTP responses from standalone mounted frontends with responses through two
stateless delivery senders. It uses the real broker and offline source/encoder
bytes. For each protocol it checks master/media playlists, segments, direct
files, seven grant/account credential envelopes, GET/HEAD/OPTIONS, Range/206/416,
response/security/CORS/cache headers, viewer and policy revocation, wrong grants,
protocol isolation and generation fencing. It closes one delivery listener and
fetches the next segment through the other. This is offline path evidence,
not a physical-node loss or a decoded playback measurement. Repeat these URLs
through the real Ingress and client probe during delivery-node loss; combine
with the durable neutral-session restoration gate for controller loss.


## Release loss matrix and verdicts

Record Plex × {source, transcode, delivery, controller} × {force-delete, pause}
× {startup, steady}. Also record Jellyfin controller and transcode loss, an Emby
baseline, physical second-host pause and native GPU kill with CPU recovery.
Expand Jellyfin action/phase coverage explicitly in the report; do not infer it
from Plex. A baseline is not a loss case. A paused lab node/container is not an
actual physical power cut; state the precise fault domain and surviving database.

Every required case starts PENDING. A case can pass only with a harness report
identifying protocol, session, candidate revision, role/boot/generation, fault
action and timing, and measuring all of the following:

- Zero stalls: the simulated player's forward buffer never empties.
- Every expected segment index fetched exactly once with HTTP 200.
- Strict decode across the splice without errors; PTS discontinuity no greater
  than one frame duration.
- Copied audio has an exact ordered AU sequence with no duplicate or gap.
  Independently encoded AAC instead uses the distinct sample-clock and PCM
  continuity oracle in [splice verification](splicecheck.md#re-encoded-aac).
  Neither packet DTS monotonicity nor an inapplicable AU-equality flag suffices.
- Server interruption below the measured client buffer, with the interruption
  definition and observation interval recorded.
- Independent placement, fencing and retirement observations for the exact
  execution; surviving healthy encoders must not restart on controller loss.
- Every required control attempt succeeds, including progress and Stop. Preserve
  `allRequestsPassed`, failed-attempt count and raw attempts independently from
  any narrower `passed` field. Raw status 0 is a transport/body failure, not an
  HTTP response or proof of denial.
- Cleanup meets the measured deadline with exact retirement, zero current and
  displaced claims, and owned-process observations. Stop acknowledgement alone
  is not retirement; later normal release remains a separate follow-up.

Missing exact-reference checks are UNVERIFIED, even when structural timing and
decode pass. A failed metric is FAIL. An uninjected case stays PENDING with its
blocking reason. A detected stall in a deliberate negative control proves the
oracle detects that fault, not that production recovery passes. Repeat reference
and fault runs on the same reviewed candidate; never combine segment sequences
of different lengths as an exact continuity proof.

The release worker maintains `evidence-index.md` and `evidence-index.json` in its
private BB thread storage. Each row includes role, action, protocol, phase,
session, server interruption, client stalls, segment/PTS/decode/AU checks,
verdict, harness report paths and SHA256s. Unknown metrics are null, never zero.
Supporting baselines, local splice experiments and superseded runs are labeled
separately. Hash the actual report files; a directory name or a worker summary
is not a report digest. Record source revisions and capture times so mutable
reports cannot silently replace the evidence behind a decision. The live index
and private media, credentials, logs and dumps must not be committed.

## Evidence scope and current release limits

Freeze each report's runtime, profile, tool versions and hashes. Record elapsed
fault time from client start and first-segment receipt for startup cases; retain
raw `early` labels when mapping them to the startup matrix. Constant-grid
startup is separate from FastStart/ramp. Record conservative interruption bounds
as bounds, leave unknown timing null, and never substitute client stall duration
for server outage duration. A controller or delivery handover that preserves a
healthy encoder has no replacement segment watermark; mark it inapplicable.

A cancelled checker or passing checks on a fetched prefix do not qualify the
full playback window. Preserve the raw reported verdict alongside stricter
component results when they differ. An attempt aborted before fault dispatch
leaves the required loss row PENDING, with the attempt and cleanup evidence
separately indexed. Later attempts never erase the original report. A later
normal zero-claim snapshot does not turn a cleanup-deadline FAIL into PASS.

### v0.3.5 matrix (2026-11-07)

All 25 software rows and all 5 mode gates pass. Every row ran on the two
generated-media labs (lab1: three-host k3s, Plex; lab2: single-host k3s,
Jellyfin/Emby) with all eight media checks, successful controls and Stop.
Cleanup is the time from the later of the Stop response and fault restoration
to the first sample showing the session retired with zero current claims, zero
displaced claims and zero owned FFmpeg processes; the bound is 15 s. A
negative value means that state was already reached before the paused
controller was restored.

Runtimes: `7e4f8c2853`, `ec7100a30c`, `f5c8c9407e` (terminal coordination kept
until displaced workers retire) and `9432615f08` (bounded copy admitted through
the distributed start check). Neither later fix changes the path of a row that
passed on an earlier runtime.

| Row | Runtime | Segments | Stalls | Cleanup |
| --- | --- | --- | --- | --- |
| Plex source force-delete startup | 7e4f8c2853 | 29 | 0 | 4.2 s |
| Plex source force-delete steady | 7e4f8c2853 | 29 | 0 | 4.4 s |
| Plex source pause startup | 7e4f8c2853 | 29 | 0 | 4.4 s |
| Plex source pause steady | 7e4f8c2853 | 29 | 0 | 3.6 s |
| Plex transcode force-delete startup | 7e4f8c2853 | 28 | 0 | 4.5 s |
| Plex transcode force-delete steady | 7e4f8c2853 | 29 | 0 | 4.5 s |
| Plex transcode pause startup | f5c8c9407e | 25 | 0 | 2.6 s |
| Plex transcode pause steady | f5c8c9407e | 29 | 0 | 2.2 s |
| Plex delivery force-delete startup | f5c8c9407e | 29 | 0 | 3.1 s |
| Plex delivery force-delete steady | 7e4f8c2853 | 29 | 0 | 5.0 s |
| Plex delivery pause startup | 7e4f8c2853 | 28 | 0 | 1.9 s |
| Plex delivery pause steady | ec7100a30c | 29 | 0 | 1.5 s |
| Plex controller force-delete startup | ec7100a30c | 29 | 0 | 4.1 s |
| Plex controller force-delete steady | ec7100a30c | 29 | 0 | 4.5 s |
| Plex controller pause startup | ec7100a30c | 28 | 0 | 1.4 s |
| Plex controller pause steady | 7e4f8c2853 | 29 | 0 | 0.5 s |
| Jellyfin transcode force-delete startup | 7e4f8c2853 | 29 | 0 | 3.7 s |
| Jellyfin transcode force-delete steady | 7e4f8c2853 | 30 | 0 | 3.8 s |
| Jellyfin transcode pause startup | f5c8c9407e | 27 | 0 | 2.9 s |
| Jellyfin transcode pause steady | f5c8c9407e | 30 | 0 | 2.4 s |
| Jellyfin controller force-delete startup | ec7100a30c | 30 | 0 | 3.9 s |
| Jellyfin controller force-delete steady | f5c8c9407e | 30 | 0 | 2.4 s |
| Jellyfin controller pause startup | f5c8c9407e | 29 | 0 | -0.1 s |
| Jellyfin controller pause steady | f5c8c9407e | 30 | 0 | -1.2 s |
| Emby baseline (no fault) | f5c8c9407e | 30 | 0 | 2.8 s |

| Mode gate | Runtime | Segments | Stalls | Cleanup | Recovery |
| --- | --- | --- | --- | --- | --- |
| Copied-AAC ramp, encoder deleted before segment 1 | f5c8c9407e | 25 | 0 | 2.9 s | resumed at 1 |
| Copied-AAC ramp, encoder deleted before segment 2 | f5c8c9407e | 25 | 0 | 2.8 s | resumed at 2 |
| Copied-AAC ramp, encoder deleted before segment 3 | f5c8c9407e | 25 | 0 | 2.8 s | resumed at 3 |
| AC3 5.1 to AAC stereo, steady encoder deletion | f5c8c9407e | 24 | 0 | 3.2 s | cut at 14: no missing or overlapping PCM samples, SNR 38.5 dB, no click |
| Bounded copy (video and audio copy), steady encoder deletion | 9432615f08 | 50 | 0 | 4.5 s | resumed at 27 on the 1.92 s source grid |

For the ramp gates the client is held after segment N-1, the original encoder
container is shown gone before N is released, and the replacement resumes at
N. The bounded-copy fixture is a 96 s, 25 fps, 640x360 MPEG-TS source with an
IDR every 1.92 s. It must be at least 5 MiB, the resolver's floor for any
release, and small enough for the source owner's copy-timeline scan (32 MiB of
packet output, about 8 MB of media for 96 s); the scan skips larger sources and
ordinary copy is used instead.

Two defects were found and fixed while measuring: the controller dropped the
coordination lease once the current generation retired, so a displaced encoder
paused through Stop could not be told to stop (fixed in `f5c8c9407e`), and every
bounded-copy title answered `start.m3u8` with 404 because the distributed start
was judged before its copy grid was authored (fixed in `9432615f08`).

Not measured: native GPU encoder loss with CPU recovery, and physical host
power loss. A paused or deleted pod is not a host power cut.

The private index keeps Markdown and JSON loss matrices, per-component results,
original and later report paths, SHA256s, raw-control exceptions, unknown timing
and abort classifications. Preserve source-runtime provenance even when a
reviewed-compatible historical media reference is reused; that reference is not
a newly captured baseline for the tested runtime.

## Multihost lab scripts

`packaging/kubernetes/multihost/` provides an isolated K3s lab across hosts over
Tailscale. These scripts mutate only the explicitly named lab resources; review
private state, exact identities, resource budgets and the real verifier before
running them. They are not production cluster installers. Load private `lab.env`
through `lab-config.sh`; install/roll helpers accept only the approved original
`ether035-multihost` / `ether035-mh` / `ether035-ha` tuple or matching
`ether035-lab2` context/prefix/namespace. Standalone renderers use
`ETHER_LAB_PREFIX` and `ETHER_LAB_NAMESPACE`; keep secrets and identities local
to each lab.

| Script | Purpose |
| --- | --- |
| `node.sh MODE STATE_DIR EXTERNAL_IP [SERVER_IP]` | Create a bounded, labeled lab K3s container on each host; capture exact container/network IDs and keep token files private |
| `join.sh`, `local-agents.sh` | Join the remote agent or add bounded local agents; local agents remain one physical failure domain |
| `install.sh PRIVATE_STATE IMAGE_TAG` | Install in the selected approved lab tuple; preserve existing PostgreSQL and original fixture templates |
| `redeploy.sh PRIVATE_STATE INTEGRATION_SHA` | Build/import a reviewed candidate and update OnDelete templates; drain/replace separately |
| `heavy-io.sh COMMAND ARGS...` | Serialize heavy lab work against the shared lock/gate marker and record queue/run/total time |
| `roll-gated.sh PRIVATE_STATE GATED_IMAGE_TAG` | Operator-supplied gated image/chart; drain and UID-safe replacement, preserving identities and lab configuration |
| `long-fixture.py ROOT IMAGE` | Render the isolated 600-second 900002 source; seed with `seed-lab.py --long-fixture` and use a separate reference |
| `migrate-pg-tmpfs.sh PRIVATE_STATE` | Explicit quiesced lab-only migration with a complete private backup and retained old PVC |
| `role-loss.sh POD` | Force-delete one captured role pod with zero grace and a UID precondition |
| `role-pause.sh POD VERIFIER [ARGS...]` | Freeze the exact captured Ether task and descendants, run the verifier and restore that task on exit |
| `loss.sh FAULT STATE_DIR PEER_TAILSCALE_IP VERIFIER [ARGS...]` | Fault the captured local lab-node container; partition only its network namespace; restore on exit |
| `remote-loss.py --container-id ID --fault FAULT -- VERIFIER ARGS...` | Fault one captured second-host lab container over authenticated BB terminals and restore; pressure aborts cannot count as completed host-loss cases |
| `pressure-gate.py` | Check sustained second-host memory headroom before starting work |
| `physical-host.values.yaml` | Delivery placement overlay; verify all role placements before interpreting a host fault |
| `relay.sh`, `relay.py`, `external-private.py` | Lab transport for explicitly configured external/private endpoints |
| `teardown.sh PRIVATE_STATE` | Retire only captured owned lab resources after workloads and verifiers finish |

Keep lab reports under thread storage. Run the harness continuously across the
fault and correlate exact Pod UID/container/boot identities before and after.
Capture real physical placement rather than relying on Kubernetes hostnames.
A force-delete is an abrupt-fault experiment, not the normal rollout procedure:
normal updates use [explicit drain and UID-safe replacement](kubernetes.md#drain-upgrade-and-rollback).
Memory pressure checks and restoration outcomes are part of the report; an
infrastructure guard blocking a run leaves its acceptance row PENDING.

The lab PostgreSQL renderer uses a 512 MiB memory-backed volume with fsync,
synchronous_commit and full_page_writes disabled. Losing that pod loses its
database; these settings do not qualify production crash durability. The gated
roll helper first drains all role writers and makes a complete backup before
migrating an existing disk-backed lab DB. It preserves the old PVC and refuses
live-writer/repeat migration. It never authorizes manual ownership-row edits.
Use an archived chart matching the immutable runtime via `ETHER_LAB_CHART`;
`ETHER_LAB_IMAGES_IMPORTED=1` skips import only after independent verification.
A gated tag supplied to the helper is not itself gate or playback evidence.
The new isolated-tuple tooling port has syntax/render/mock coverage; it still
needs separately recorded live-runtime validation.
