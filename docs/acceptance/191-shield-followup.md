# #191 Troy capture follow-up — v0.3.5

Branch: `fix/191-shield-subtitles-followup`, based on `release/0.3.5`
(`af27e98fb`, including `c07c1775a`, group-3 conformance and the #339 pin).
Latest daimikegor comment read with `gh issue view 191 --comments`.
No comment posted and no issue closure requested.

## Capture scope

The unpacked support files were read as data only. `events.jsonl` records
Plex for Android (TV) 2026.20.0, Android, model `darcy`, SHIELD Android TV.
The first playback sequence is POST /playQueues, GET /library/metadata,
decision ("conversion permitted"), start.m3u8. At 21:02:53Z there is a
PUT /library/parts; the next decision takes 2.4 seconds, then another start.
`sessions.json` has no retained session details. Logs/REPORT contain no
recoverable raw request profile.

**Exact query/header replay is impossible from this ZIP:** every recorded
query is `[redacted-key]=[redacted]`, and raw client-profile headers are
absent. The lasting replay preserves recorded methods/order/client identity,
remaps the rating key to the package fixture, and reconstructs query/profile
fields from the existing Android TV contract. It adds a metadata GET before
any playback and after the selection PUT to check the reported picker issue.
The AAC-only target and explicit 8-bit limitation are authored controls to
force each conversion path, not claims about unrecorded reporter headers.

## Per-symptom evidence

| Symptom | Unmodified release | Follow-up result |
| --- | --- | --- |
| 1. Text absent during conversion | Already fixed for reconstructed request. SRT decision says copy/segments-av; start retains MKV and source subtitle index. | Same with HEVC Main 10/DV7 HDR10 + DTS-HD MA 5.1 fake facts, both HEVC copy/audio transcode and full tone-map conversion. Encoder command maps text, copies codec, sets default/language, never `-sn`. |
| 2. Picker disappears after PGS PUT | Already fixed in warm-source replay. | Both SRT and PGS keep the same positive IDs through initial metadata, queue, metadata, SRT decision/start, PGS PUT, metadata, PGS decision/start and final metadata. Variants are enabled. PGS remains selected and is burned. |
| 3. First detail open has no tracks until Play | Still broken when no exact warm/resume probe exists. Baseline replay fails both cold cases with `tracks=[] probes=0 prepares=0`; warm cases pass. Existing four-second speculative wait can expire, be disabled or be skipped during active playback. | Newly fixed for official Android TV/Shield detail requests: obtain exact source facts before the first successful metadata response. First metadata contains both subtitle rows and preference-selected English SRT with zero Prepare calls. |

Baseline check temporarily restored the three changed production files from
`origin/release/0.3.5`, ran the new HTTP and encoder tests, then restored the
fix. Only the two cold metadata cases failed; warm replay and encoder graphs
passed. The source fixture includes video 0=HEVC Main 10 3840×2160 DV7/HDR10,
audio 1=DTS-HD MA 5.1, subtitle 2=English SRT, subtitle 3=English PGS.

Recorded PMS facts checked: pre-play metadata already carries real subtitle
IDs/indexes/languages/selection; PUT selects an existing track; Matroska
conversion advertises selected text as copy/format/segments-av. No private
implementation or fixture was imported.

## Implementation and limits

`core.MetadataProber` uses the playback manager's existing resolve/open/probe
and warm cache. It reuses validated persisted probe facts when available,
does not create a playback session, write watch progress or prefetch video,
and can run when speculative caching is disabled or another title is playing.
Existing per-user/global concurrency, rate limits and source validation remain.
The request waits at most 12 seconds; an in-flight source probe may continue
within the existing 45-second preparation budget for a retry. If exact facts
are unavailable or admission is saturated, metadata returns 503 with
`Retry-After: 1`, instead of a successful response falsely claiming no tracks.
The client still needs to retry that response; physical Shield retry behavior
is not established here. This does not promise immediate tracks from an
unavailable source. A genuinely subtitle-free probed file remains valid.

The new metadata behavior is restricted to the existing official Android TV /
Shield predicate. Other clients retain their existing policy. Playback delivery
code is unchanged. Multi-key/hub listings still use the existing browse path;
this correction targets the single-item movie/episode detail endpoint.

## Gates

Only package tests were run; no repository-wide suite, build or race suite.

- `TestShieldTroyCaptureFollowup`: four cold/warm × copy/tone-map HTTP flows,
  production decision engine, fake playback source and encoder recorder.
- `TestMetadataProbeWithoutSpeculationOrPlaybackSession`: production manager,
  native source probe seam, SRT/PGS facts, speculation disabled, idle/other title
  active, no added sessions, no video prefetch, no repeated resolve/probe.
- `TestShieldMetadataProbeFailureDoesNotPublishEmptyTracks`: retryable metadata
  failure instead of a successful empty track list.
- `TestShieldTroySubtitleEncoderPaths`: SRT/PGS × HEVC copy/tone-map encode
  command graphs, including PGS overlay and text mapping/default/language.
- Relevant playback, Plex, session HLS handoff and transcode package regressions
  cover warm/resumed facts, #339 version identity, Android alternate tracks,
  selection reset, native/persisted probe contracts, MKV text seeks and GPU
  subtitle preservation. Exact command and results are in the delivery report.
- `TestIntegrationShieldHDRTextSegments`: generated HEVC Main 10 HDR10 + DTS
  and SRT source, real FFmpeg and segment muxer. Joined header and two clusters
  probe as HEVC/AAC/subrip, with timed subtitle packets. This source is 640×360,
  mono DTS, and carries no DV RPU; 4K DV/DTS-HD MA 5.1 policy uses fake facts.
  **Real tone-map subtest skipped:** local FFmpeg 8.0.1 lacks zscale. Tone-map
  decision/command graphs pass, but actual tone-mapped pixels and PGS burned
  pixels were not verified in this follow-up. Earlier delivery acceptance
  covers real text segments/PGS pixels with a different SDR source.

No physical NVIDIA Shield playback verification. These results establish the
server contracts under reconstructed requests, not the reporter's on-device
outcome or exact redacted request.

## GitHub reply draft

Thanks for the Troy recording. It exposed another gap: on a cold movie-page
open Ether could return estimated metadata with no embedded subtitles, then
show the real tracks only after playback had probed the file.

The v0.3.5 follow-up obtains those tracks before the first successful Shield
detail response. It preserves their IDs through Play and subtitle changes.
If the source cannot be probed yet, it returns a retryable response instead
of telling the player there are no subtitles.

I also replayed the recorded request order with the Shield identity and a
fake Troy-like HEVC Main 10/Dolby Vision/HDR10 + DTS-HD MA source containing
SRT and PGS. The existing v0.3.5 fixes retain SRT in Matroska during audio-only
conversion and full video conversion, burn PGS, and preserve both tracks after
switching. The ZIP redacts query parameters and omits the raw profile, so this
was a reconstructed replay. Real HEVC/HDR10 + DTS output segments retained
SRT; actual tone-mapped output could not be checked on this test machine.

The branch is https://github.com/braedonsaunders/ether/tree/fix/191-shield-subtitles-followup.
I have not verified playback on a physical Shield, so the issue should remain
open pending a device retest of first-open tracks, SRT playback and a PGS switch.

## Final package command/results

```sh
go test ./internal/playback ./internal/plex ./internal/sessions ./internal/transcode -run 'Test(Shield|MetadataProbe|PreResolve|WaitWarmProbe|CachePolicyCanDisable|MetadataListsWarm|MetadataWithVariantsListsWarm|FirstOpenMovie|ResumedTracks|VersionIdentity|VersionFlow|AndroidTrackChoice|AutoFallbackReleaseRetains|DecisionListsAlternate|PartSelectionReset|NativeProbe|Probe|HLSDecision|HLSPlanContainer|PlexStreamBinding|BuildArgsMatroskaTextTrack|GPUPipelineDoesNotDiscard|ToneMapGraphs)' -count=1 -timeout=120s
go test ./internal/transcode -run 'Test(ShieldTroySubtitleEncoderPaths|IntegrationShieldHDRTextSegments)' -v -count=1 -timeout=120s
```

Final protocol/command gate: playback 1.105s, plex 2.187s, sessions 0.816s,
transcode 1.020s — all PASS. Media gate: PASS in 11.762s, with the real
tone-map subtest explicitly SKIP as described above. `git diff --check` passed.
