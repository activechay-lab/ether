# Private HLS ramp v1

The coordinator snapshots shared FastStart configuration before initial admission.
`HLSRequest.Ramp` is a value containing Version, Segments and Duration (nanoseconds).
The frozen request also contains the steady SegmentDuration and immutable MediaInfo.
Together these reproduce the existing deterministic Timeline, including folded tails.
Recovery retains the request and changes only Resume's first segment/generation.
The worker uses the authored geometry instead of its current FastStart config.

AuthoredRamp must be negotiated by both ends. Version 1 supports 1–16 ramp
segments, at least 100ms and shorter than the steady duration (at most 1 minute).
It is bounded to finite zero-offset encoded H264 HLS, MPEG-TS or fMP4, with the
startup-probed libx264 encoder. Copy, live, soft subtitles, native enhancement,
RecoveryStart and unknown versions are excluded. Actual ramp/steady and seek
commands are checked against inventory before source resolution. The existing
cluster recovery admission remains copied-audio MPEG-TS; this change does not
admit new audio/copy recovery modes or hardware ramps.

Zero is omitted from JSON and preserves legacy wire hashes. A legacy private
Start is translated only at backend invocation to explicit version-1 steady
planning; it never inherits a newly enabled worker FastStart setting. Local
non-private callers with zero retain existing config-driven behavior.

Validation combines fake-runner all-index resume/playlist/command invariants with
configuration drift, bounded request rejection, negotiated wire transport,
durable admission round-trip and a fake cluster encoder-loss replacement case.
The existing 36/36 local SIGKILL media proof is archived at
`/data/bb/thread-storage/thr_i22bncjuty/{REPORT.md,final-matrix.json}`; it is not a
cluster acceptance result for this change.

After the next coordinated build, the smallest cluster gate is one immutable
local HTTP-range fixture (at least 29 seconds, CFR H264 + AAC), software H264
transcode with copied AAC, MPEG-TS, FastStart 2x2s then steady 4s. Freeze the
initial manifest and title boundaries before fault injection. In separate
sessions remove only the owned encoder at the first missing finalized segment
N=1 (within ramp), N=2 (transition), and N=3 (steady). Verify the actual replacement
Start has the same Ramp, SegmentDuration, source revision and media plan; its
Resume is published-floor+1, with advanced generation and normal retirement
fencing. Compare the seam's decode, IDR/closed GOP, PTS/frame counts and AAC AU
hashes with the uninterrupted reference, plus identical manifest boundaries and
no skipped/repeated index. Exercise one replacement with different worker ramp
settings to demonstrate that the admitted plan wins. Run through the normal
fixture source/grant/placement path, with the harness's marker/flock, without
manual claims or DB edits. fMP4 cluster recovery is not enabled by this slice.
