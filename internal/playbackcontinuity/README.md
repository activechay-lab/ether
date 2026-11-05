# Proof-gated continuity policy

This additive, opt-in component does not change normal Plex playback. It owns
only its migration and durable policy. It is **not a seamless recovery engine**:
no encoder starts, source reopens, capacity releases, manifest rendering, network
fencing, buffer retention, or background goroutines occur here. No consumer is
wired by this change. Dependencies are explicit `*sql.DB` and trusted interfaces.

## Contract and replay

1. `Open` pins stable public session/grant **paths**, the exact source owner boot,
   PlayID, immutable revision and associated execution `placement.Fence`.
   Callers must already have placement authority. Grants remain authenticated by
   delivery; these paths do not authorize a request.
2. `RecordDelivery` advances sequence and timestamp floors using authenticated
   **completed final HTTP output** evidence. Worker `Observe` DeliveredUntil,
   Goodput, encoder readiness and files on a worker are not this evidence.
   Completed HTTP output does not establish client retention or decoding.
3. `ReserveRecovery` validates the old exact fence, source binding and seek epoch,
   authenticates final output, requires BOTH source and encoder retirement or
   external authenticated fencing covering both exact hosts/boots, and verifies
   a measured buffer budget. It persists an immutable token and freezes publication
   floors. **Already committed buffered output remains deliverable**; re-serving
   those bytes does not advance the allocator. Replacement output waits behind
   the delivery publication barrier. This token is not an encoder-start permit.
4. After separately authorized admission/start, `CommitRestart` validates the new
   higher generation and held first packaged segment, including real muxed clocks
   and independent decoding across all required tracks/renditions. Its sequence
   equals the next unused floor and output start equals the previous exclusive
   timestamp end. The exact source position must match the reservation. Timestamp
   offset is descriptive evidence of actual media, never an instruction to fix
   timestamps solely in a manifest. Delivery publishes only after this commit.

Output sequence and timestamp floors never decrease, including seeks. A seek
increments the durable epoch and changes title position; real muxed output must
use the allocated output clock. The mapping requires HLS discontinuity/init
handling or a new DASH Period with correct presentationTimeOffset. Init, segment
and manifest requests must preserve the public URI/grant while selecting the
correct generation/epoch behind it. Keep immutable segment bytes addressable for
old requests and buffer retention; never overwrite a public sequence.

Every mutation uses one SQL compare-and-swap against the durable row revision.
No process mutex guards a shared invariant. SQLite and PostgreSQL 14 use the same
additive TEXT/BIGINT schema and ON CONFLICT/UPDATE operations through Ether's
placeholder-rebinding driver. No PostgreSQL server was used in offline validation.
Rows/floors are retained; deleting them permits identity reuse and is not allowed.

Identical Open replays never reset state. The latest reservation and commit replay
immutably, including after service recreation. An older token replaced by a newer
transition fails closed; conflicting payloads fail. Replay success is historical,
**never fresh placement/execution authority**. SQL write errors return
`ErrUnknownOutcome`: Lookup and retry the same immutable request; do not infer
that a write failed or start another process. A crash while reserved remains
reserved until a matching verified restart commits. No automatic abort/unfreeze
exists. Parent orchestration must reconcile held output and execution identities.

## Proof providers and viable recovery

The trusted EvidenceVerifier must bind every assertion to the exact request,
fence, source identity, epoch and final delivery authority. Opaque proof strings
are references, not self-authenticating attestations. Timeouts, unreachable nodes,
expired leases, vanished Pods and stale heartbeats are never retirement proof.
A trusted lab can attest a physically killed process/host and exact boot identity;
an arbitrary network partition cannot authorize duplicate execution.

Source affinity changes require optional `SourceReopenVerifier`, absent by
default, authenticating old/new Owner+boot, PlayID, associated fences and the same
immutable revision. A production provider must identify canonical immutable remote
release/media bytes **and timeline/tracks**, verify that revision on reopen, and
fail if exact reopen is unavailable. Release ID, URL, filename, or matching file
path alone does not suffice; filesystem device/inode identity is not cross-host
proof. No authoritative provider currently exists in this package. Source-host
loss is unsupported without it and a real hard-loss lab result.

Required surviving buffer is the overflow-checked sum of measured conservative
detection + fencing + exact reopen + first independent encode + publication +
safety bounds. Every term must be positive and the deployment-specific measurement
must be authenticated. For example, bounds 2+3+2+4+1+2 seconds require 14 seconds
of independently retained, contiguous playable output. This is arithmetic, not a
measured Ether result. The trusted provider must distinguish proxy-retained bytes
from client buffering, account for elapsed time/freshness and reject an exhausted
budget again at restart publication. Unbounded partition/fencing time has no
finite seamless buffer budget and must fail closed.

Parent integration needs independent surviving delivery storage/proxy retention,
continued service of buffered committed segments, final HTTP instrumentation,
publication barriers with crash reconciliation, grant/epoch mapping, authoritative
reopen, real mux clock control, and measured hard-node-loss tests. The lab must
record worst observed recovery bounds, buffer exhaustion, source and encoder
process counts, final sequence/PTS/DTS continuity, and physical-client playback.
Allocator unit tests alone establish none of those end-to-end claims.

## Current encoder investigation

Inspected existing code (no live encoder run or new claims of measurement):

* `transcode/session.go`: HLS uses `base+n*segDur`; DASH uses title base zero and
  separate representations. Numbering alone does not establish media continuity.
* `transcode/args.go`: HLS uses `-start_number StartSeg`, `-copyts` and selected
  `-output_ts_offset`; fMP4 uses `frag_discont+skip_sidx`, with specific negative
  DTS normalization. Encodes force planned keyframe boundaries; copies cannot
  manufacture a keyframe at an arbitrary requested timestamp.
* `transcode/chunks.go`: keyframe-indexed HLS cuts into short chunks and slots by
  actual timestamp, drops copy preroll, and may repeat GOPs without an index.
  MPEG-TS has a documented 1.4s mux delay unless `mpegts_copyts` is enabled.
* `transcode/dash_session.go`: each run's representation chunk counter starts at
  one, then the slotter maps actual timestamps to session segments. The current
  generated MPD has one Period; adding a recovery Period requires a delivery seam.
* `transcode/fmp4_edit.go` and `fmp4_open_gop_test.go`: fragment decode-time/edit
  correction and open-GOP leading pictures already require real media inspection.

An implementable adapter must reuse/extend those mux/slotter controls and prove
all audio/video track PTS/DTS/tfdt (with timescale and MPEG-TS wrap normalization),
independent restart, retained init compatibility and exact title resume. If the
requested boundary cannot be encoded independently on the required actual output
clock, return unsupported. A manifest-only rewrite cannot satisfy this contract.

Focused offline tests cover retirement/final-delivery proof rejection, overflow,
buffer refusal, phase freeze, replay after service recreation, seek epochs,
generation/sequence/timestamp floors, cross-owner attestation and CAS races.
