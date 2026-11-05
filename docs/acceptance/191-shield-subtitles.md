# Shield embedded subtitles — #191 / v0.3.5

Branch: `fix/191-shield-embedded-subs`, based on `release/0.3.5`
(`95d6c007a`). Full issue body/comments read before investigation. At the time
of investigation GitHub reported the issue OPEN. No issue comment was posted.

## Reproduced causes

1. A Plex Android TV MDE decision can accept native MKV playback, followed by
   `start.m3u8` with direct play still allowed. Ether's `sessions.HLSDecision`
   fallback unconditionally changed that decision to MPEG-TS. The adapter's
   callback checked the audio target but never restored the negotiated HLS
   container. The text subtitle index survived, but `matroskaTextSubtitle`
   only maps subtitles for MKV; MPEG-TS therefore ran with `-sn`.
   This path matters for stable files, including HTTP/debrid sources that do
   not take the source-change remux override. Earlier subtitle tests checked
   decisions and explicit MKV remuxes, not this late handoff.
2. `subtitles=none` dropped every embedded bitmap subtitle's encoder index.
   The selected XML row could remain selected while the encoder received -1.
   Moreover, the segmented muxer carries text only: retaining a PGS index
   alone would still deliver nothing. Conversion needs an actual burn.
3. Explicit text burn requests were implemented only for the browser player.
   The TV player's burn setting now also enables the existing text burn path.

The new test failed before the fix with SRT/ASS `Container:mpegts` on the
late handoff and PGS `SubtitleStream:-1` for all three playback paths. After
the fix these are MKV with the chosen index, and converted PGS is burned.

## Wire contract and references

`testdata/pms` no longer exists in this release. Ether's recorded PMS 1.43.4
references are in `testdata/conform/plex` (see `internal/plex/conform_test.go`).
Those are useful for decision envelopes and PGS metadata, but do not contain
the exact Shield subtitle conversion capture.

Additional recorded PMS 1.42.2 conversion facts (Android Mobile, iOS and Web)
and a sanitized Shield decision request were studied. The lasting test authors
its own media facts and request from these observed protocol fields.

| Element/path | Native MKV | Matroska HLS conversion |
| --- | --- | --- |
| Part Stream | `streamType=3`, stable positive int32 ID, source codec/index, languageTag, source forced/default flags | Same selected ID; copied text loses source index, gains `format`, `decision=copy`, `location=segments-av` |
| Selection | Exactly the picked track is selected; `subtitles=none` is delivery control, not Off | Chosen index reaches encoder; track is mapped, flagged default, language restored |
| Embedded `key` | Absent | Absent for in-segment text; no extraction URL required |
| SRT/ASS | Client demuxes/draws in-file tracks | Selected text copied into MKV, ASS script header retained |
| PGS | Shield may draw in-file PGS; preserve its selected index | Burn into video; HLS decision says `burn=1`, `decision=burn`, `location=segments-av` |
| Playlist | Part bytes | Existing Matroska HLS header via `EXT-X-MAP`, then cluster fragments |
| `/library/streams/{id}` | Existing authenticated extraction route remains available for text | Used when a decision really offers a sidecar; never a substitute for PGS burn |

PMS makes the output contract explicit: a Matroska target stays Matroska;
selected text travels with the AV segments. Ether already had the muxer,
subtitle mapping, default/language flags, ASS headers and header/cluster
playlist support. Its late fallback bypassed those facilities. The conversion
plan now explicitly separates image burn from text copy and sidecar delivery:
Ether retains the TV image selection and burns it when conversion is needed.
Explicit TV burn requests use Ether's existing libass path.

AndroidX's Matroska extractor recognizes S_TEXT/UTF8, S_TEXT/ASS and S_HDMV/PGS.
Text block durations and ASS initialization data matter, so merely finding cue
text in output bytes is insufficient. Primary reference:
https://github.com/androidx/media/blob/release/libraries/extractor/src/main/java/androidx/media3/extractor/mkv/MatroskaExtractor.java

All new playback policy is gated to official Plex Android TV products or an
official Plex Android product reporting a Shield. Mobile, Web, iOS, tvOS and
Plezy retain their existing response/delivery behavior. No core/config changes.

## Verification

- Lasting `TestShieldSubtitleDeliveryContract`: production decision engine,
  HTTP metadata/decision/start handlers, Generic profile, HLS MKV target,
  `hasMDE=1`, `subtitles=none`, `advancedSubtitles=text`, SRT/ASS/PGS, native
  selection and late HLS handoff, remux, scaled transcode, repeated session
  track changes, explicit text burn requests and Off. Checks metadata indexes/IDs/languages/flags and
  conversion rows plus the actual encoder request, not just selected XML.
- Local replay: generated an eight-second black H.264/AAC MKV with authored
  SRT, ASS and a 32x8 PGS rectangle visible at 1–3 seconds. Real ffprobe
  confirmed streams 2=subrip, 3=ass, 4=hdmv_pgs_subtitle. Replayed the Shield
  request through the HTTP handlers with the real FFmpeg 8.0.1 manager.
  All nine combinations (three tracks × late-HLS/remux/transcode) passed.
  Joined initialization header and first cluster segment were probed: SRT/ASS
  had cue PTS and duration. Decoding PGS output at two seconds found 256 bright
  pixels at 640x360 and 64 at 320x180. The temporary lab harness was removed;
  it is not a second CI regression test.
- Targeted package gate: new contract plus existing PMS decision, direct-play
  subtitles, direct-stream subtitles, extraction, slow response, Android track
  choice/alternate tracks and enhanced-player target tests passed (6.336s,
  including the local real-media replay). The final committed targeted gate
  passed again after adding explicit burn/Off coverage (0.643s).
- `go test ./internal/plex -short -count=1 -timeout=120s` timed out during
  `TestShelfGenerationPagingInvariant`. Before timeout it also reported
  `TestVirtualChannelDASHPlaysThroughPlexWeb`: segment HTTP 503. This broader
  package gate is not green; neither result was investigated or attributed
  to a baseline in this subtitle-scoped change.
  No repository-wide build, test or race suite was run.

Physical Shield playback has **not** been verified. This proves the reproduced
server-side failures and media output, not the reporter's on-device result.
Do not close the issue or claim Shield verification from these checks.

## GitHub reply draft

The earlier fixes missed a playback path. I reproduced a server-side failure
using the Shield's Android TV request profile: the decision accepted MKV, but
when playback started as HLS Ether switched it to MPEG-TS. That discarded the
selected SRT/ASS subtitle. The previous tests stopped before this handoff.

The v0.3.5 fix preserves the negotiated Matroska HLS target and the selected
text track through playback start. I also fixed PGS selection being discarded:
converted playback now burns PGS into the picture, and the TV player's explicit
text burn setting is honored. Other clients keep their existing behavior.

I tested a generated MKV containing SRT, ASS and PGS through all three server
paths. The text cues survived in the output segments, PGS was visible in decoded
video frames, and switching tracks and Off passed the protocol test. I have not
verified this on a physical Shield, so I won't call the device issue resolved
or close it based on these offline results. [fix/191-shield-embedded-subs](https://github.com/braedonsaunders/ether/tree/fix/191-shield-embedded-subs)
is the exact branch to test; the remaining confirmation is selecting SRT,
ASS and PGS on the Shield during playback.
