# Deep-join acceptance (P6)

P6 is merged into integration as `de6f6d680`; its implementation commit is
`9d74cac8a`, rebased on `eda56538d`. The release's Plex first-play, subtitle,
cluster and per-viewer catalogue behaviour is retained. P6 changes no Plex
response shapes or Plex test assertions.

## Input preparation

A Matroska/WebM decoder uses the generation's saved container cue immediately.
The playback manager prepares a local loopback projection containing the codec
headers followed by the keyframe cluster at or before the join. Original-file
SeekHead/Cues are replaced with Void and the projected Segment is unknown-sized.
The decoder trims the small forward remainder instead of probing/seeking
through the original file. Cluster offsets must match the published index;
header reads/cache are bounded to 8 MiB. Unsupported formats retain their
standard seek path and can use the existing cached indexed warm windows.

The existing parallel seek warmer starts the requested keyframe span while
headers prepare. Background prewarming rolls a 45-second target window every
15 seconds for the two most recently watched virtual channels, capped at
32 MiB per window and two rolling jobs server-wide. The existing engine cache,
background priority and provider/host connection caps remain authoritative.
Warming creates no playing viewer session, speculative decoder or encoder.
Programme-boundary decoder preparation remains twenty seconds ahead.

`core.PrewarmOptions.StartOffset` is the only additive core change in P6 and is
excluded from frontend JSON. `DecoderRequest.Seeked` is an internal trusted
projection marker. No configuration changes or migrations were added.

## Cold ident and instrumentation

Only the unscheduled cold-output ident is fill: at least one second on screen,
at most four seconds. A ready programme takes over with 300 ms of video and
continuing-clip audio crossfade. Scheduled moments keep their saved durations.
Programme frames catch up to the wall clock while the prefix fills. An already
running shared output creates no ident for a new viewer.

Logs distinguish the first usable decoder frame, first aired programme frame
and whether a viewer attached to an existing output. The client harness records
completed frame decoding separately. It checks for non-black stream content and
writes actual nonempty PNGs; it does not infer client readiness from a successful
HTTP response alone.

## Lab measurements

Real debrid Matroska, 1920×1080 shared VideoToolbox output, 30 fps. Times start
immediately before the tune POST and exclude login. Persistent probe/index
metadata and the configured 4 GiB cache were retained; no cache purge was done.
The cold range incurred provider connection waits. This is an observed lab
result, not a claim that arbitrary upstream response times are bounded.

| State | Join offset | Decoder ready | First aired programme | First stream client frame | First programme client frame |
| --- | ---: | ---: | ---: | ---: | ---: |
| Cold output, new indexed range | 1h32m16s | 1.779 s | 1.803 s | 1.360 s | 4.368 s |
| Cold output, source/index window prewarmed | 1h39m58s | 0.178 s | 1.104 s | 1.409 s | 2.376 s |
Running shared output (same deep programme):

| State | Decoder ready | First aired programme | First stream client frame | First programme client frame |
| --- | ---: | ---: | ---: | ---: |
| Steady live-edge join | already ready | already airing | 0.212 s | 0.281 s |

The cold programme client frame is sampled after the complete 300 ms crossfade.
The second row is still a cold output and therefore observes the one-second
ident minimum and completed-segment publication; it is distinct from an
already-running channel. In the running row no decoder or encoder starts.

The same authenticated client seeded the final output, kept polling for twelve
seconds and then reopened at the live edge. This avoids repeated logins and
proves output reuse (`running=true`). The steady sample visibly shows the film
and corner logo, with no ident. The main cold sample visibly shows the real film.

An immediate second viewer during an output's opening seconds can decode a
frame from the **already-aired global crossfade** in the latest completed
segment. No new prefix is created. One such join measured 0.149 s to stream and
0.215 s to programme; its first cached frame contained the existing fade. The
steady table above is reported separately rather than hiding this distinction.

Artifacts under the private `v035/lab/pass` directory:

- `p6-final-cold/latency.json`, `stream-first.png`, `programme-first.png`.
- `p6-steady-pair/cold/latency.json` and frame PNGs.
- `p6-steady-pair/running/latency.json` and frame PNGs.
- `latency.py` and `latency_pair.py` contain the reproducible client method.

## Verification

Build all, CGO-disabled CLI build and vet pass. Scoped short suites pass:
Live TV 22.638 s, playback 38.822 s, app 42.806 s, Plex 34.700 s,
transcode 12.855 s and debrid 86.256 s. The real TS/fMP4/Matroska continuity gate
passes in 40.965 s (cold finished segment 1.760 s, running playlist 135.5 µs).

The lasting real Matroska test selects a preceding keyframe, trims across a
colour change to prove the requested instant, and rejects a non-indexed offset.
The cold-ident test verifies the one-second minimum, four-second cap and actual
300 ms video/audio transition. Existing no-look golden, reel/plugin clock,
unrated policy and source-retirement checks remain green.

The preceding isolated stale-live fix is `49b00bee4`, cherry-picked into
integration as `eda56538d`. Its real eight-segment still-served HLS test proves
that reconnects stay on Signal until genuinely newer source PTS arrives.
