# Plex Android posters and subtitles — #341 / v0.3.5

Base: `release/0.3.5` at `45eab97cf`. Branch:
`fix/341-android-posters-subtitles`. Issue #341 was read in full: it has no
comments, version, connection information or support capture. v0.3.4 is an
investigation assumption, not a reported version.

## Evidence and limits

The committed `testdata/conform/plex/manifest.json` lists Web, iOS, Apple TV
and one decision capture. There are **no Android request captures** in that
corpus or Android Plex profiles under `internal/conform` on this base.
Do not describe the new tests as recorded Android requests. Existing PMS
1.43.4 metadata/decision comparisons and the documented PMS 1.42.2 subtitle
conversion facts in #191 supply the existing wire contract. New image requests
are explicitly reconstructed scenarios. No live server or device was used.

The reporter's exact poster failure remains ambiguous. The tests demonstrate
server defects, not that the reporter used those precise requests.

## Reproduced server defects

- #191's delivery and detail-probe corrections were gated by `androidTVClient`.
  Official `Plex for Android (Mobile)` missed PGS selection/burn, explicit text
  burn, the late direct-play-to-HLS Matroska restoration and the detail-page
  probe. Extending the existing decision/start and cold/warm detail tests to
  Mobile covers these paths, track changes and Off.
- The photo handler treated an absolute URL on another connection hostname as
  a foreign proxy request: an authenticated request for
  `https://previous-connection.invalid/library/metadata/<key>/thumb/<stamp>`
  returned 403 even though the relative URL and a `plex.direct` URL worked.
  This models switching local/remote/relay connection bases; it does not prove
  an actual Plex relay session. Android now resolves recognized local artwork
  routes under the outer viewer without dialing the nested hostname. Unknown
  routes/remote image hosts retain the proxy restrictions.
- `format=jpeg` was ignored: a PNG upstream yielded PNG bytes and Content-Type.
  The Android photo path now converts explicit JPEG/PNG output requests, with
  a decoded-pixel limit. The upstream cache remains format-neutral. WebP can
  be decoded; AVIF is not introduced as an output format. There is no recorded
  AVIF request or response in this issue to justify an AVIF-specific change.

## Other poster checks

- Existing integer width/height and 0/1 minSize/upscale requests remain valid;
  malformed values retain validation. No captured Android boolean-string
  grammar is available to justify relaxing validation.
- Both light and detail metadata use relative, token-free thumb/art URLs;
  clients attach their own credentials. Tests exercise both URLs through the
  photo wrapper. Nested owner tokens cannot grant the outer viewer access.
- Provider URLs and API keys remain server-side (`Images.GetPoster`), and
  provider poster output is already JPEG. Providers refuse redirects and use
  the TMDB fallback on failure; there is no provider redirect for the Android
  app to follow. Existing discovery/provider tests cover this path. Changing
  provider redirect policy would affect every client and is outside the
  demonstrated Android failures.
- No changes to Plex Web, iOS, Apple TV or Plezy policy. No device capture was added. Only package-scoped Go tests were used.

## Gates

- Against the unmodified release handlers, the expanded tests fail: Mobile
  detail reports `tracks=[] probes=0 prepares=0`; the late HLS encoder receives
  `Container:mpegts` for SRT and ASS, explicit burn remains false, and PGS rows
  remain `decision="ignore"`. Both Android products return PNG to `format=jpeg`
  and return 403 for the different-connection artwork URL. Existing TV paths
  and the Web/Plezy image controls remain passing.
- Focused corrected tests passed (`internal/plex`, 1.787s) before expanding the
  image gates to include provider JPEG-to-PNG and HEAD behavior.
- Full package gates passed: `internal/artwork` (0.980s),
  `internal/artwork/service` (0.963s), `internal/conform` (0.384s),
  `internal/conform/plex` (3.085s), each with `-count=1 -timeout=120s`.
- Broader Plex package gate passed (4.075s):
  `GOMAXPROCS=2 go test -p 1 ./internal/plex -run 'Test(Android|ShieldSubtitleDeliveryContract|ShieldTroyCaptureFollowup|Photo|.*Subtitle.*|.*Artwork.*|.*ConformsToPMS|ServerMediaURLsOmitAccountToken|DecisionListsAlternateTracksForAndroidOnly|DirectStreamCarriesSelectedTextSubtitle|MetadataListsWarmProbedTracks|MetadataWithVariantsListsWarmProbedTracks|FirstOpenMovieAndShowSeasonWarmProbedTracks)' -count=1 -timeout=180s`.
  This is a selected package gate, not the full Plex suite. No repository-wide
  suite or build was run. Physical Android phone/TV playback remains unverified.

## GitHub reply draft

Thanks for reporting this. I found that the Android TV subtitle fixes in
v0.3.5 did not also apply to Plex for Android phones. The fix extends the
Matroska text-subtitle handoff, PGS burn when conversion is needed, explicit
text burn, and detail-page track probing to the phone app too.

I also reproduced two image-handler failures: absolute artwork URLs using a
different connection hostname were rejected, and explicit JPEG requests could
receive another image format. The Android fix handles those cases while
keeping artwork authenticated. These are offline server tests; I have not
confirmed your exact failure on a phone or TV.

Which Ether version are you running, and does this happen locally, remotely,
or both? If it still happens on v0.3.5 with this fix, please attach a support
ZIP captured after opening an affected poster and trying a subtitle, and
include the Plex app version and phone/TV model. That will let us distinguish
these fixed paths from a different image request or playback profile.
