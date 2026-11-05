# Issue 315: playback failure delivery (v0.3.5)

Scope: on-demand Plex playback. Branch `fix/315-client-visible-errors`, based on
`release/0.3.5`. Read the issue body and every comment before implementation.

## Evidence and cause

The reporter uses Fire TV Cube gen 3, Fire OS 7.7.1.5, Plex 10.30.9.4262.
They report readable reasons on iPhone/iPad, but indefinite buffering on Fire
Cube. The supplied server log is evidence of server events, not client HTTP
responses or client rendering. On November 5 it records `infringing_file` at
12:10:43.632 and 12:10:44.558, pre-admission decision failures at 12:23:49.628
and 12:44:57.922, and account-wide add pauses at 12:25:08.413 and 12:46:11.399
(times UTC-05). No client trace establishes the Cube's exact retry algorithm.

Three code paths explain how the server can prolong buffering:

- `awaitSource` errors precede the binding claim in `prepare`. `transcodeTable.fail`
  delegated to `BindingTable.Fail`, which ignores missing bindings. The following
  start therefore lost the completed error and could search again.
- A remembered failure was consumed by the first start; retries searched again.
  Failed starts/parts returned empty 503 responses with `Retry-After`, explicitly
  inviting retries. Termination text was only queued at start, so a client that
  stopped after decision could miss it entirely.
- Starts without decision and direct part reads used unbounded `Prepare` for
  acquisition, whereas automatic decisions used the 12-second bounded interface.

Separately, Real-Debrid's classifier correctly maps code 35 to `ErrBlocked`, but
`Acquire` replaced it with `ErrAddRefused` at addMagnet. Two different permanently
blocked releases could trigger account-wide add backoff. The provider rejection
was being treated as a temporary account problem instead of a per-release block.

## Exact HTTP contract

| Path | Before | After |
| --- | --- | --- |
| `universal/decision`, failed resolution | HTTP 200; general 2000, direct-play 3000, transcode 4000; every corresponding Text contains `playbackMessage(err)`; MDE 2000/Text when `hasMDE=1`; metadata without playable Media | Same response shape, codes, types and text; also queue timeline termination immediately; claim missing failure binding |
| `universal/start.m3u8`, `start.mpd`, progressive `start`, failed preparation | Empty 503 plus Retry-After for unavailable/unreleased/auth/source/timeout/slow/stream-limit; default empty 500; not-found 404; unauthorized 401 | Empty 404 for errors wrapping not-available or not-found; 403 for unauthorized; otherwise 400; no Retry-After |
| `/library/parts/...`, failed preparation | Same start-error mapping; unbounded source wait | Same terminal mapping as start, bounded source wait; reuse recent matching decision failure where session identity matches |
| `/:/timeline` after failure | HTTP 200, terminationCode 2006/Text after a failed start | Same shape, also available immediately after failed decision |

`directPlayDecision` is not a separate wire attribute: the container has
`directPlayDecisionCode`/`directPlayDecisionText`. Failed decisions have no playable
Media/Part decision. Media success decisions remain unchanged.

An unavailable source may become playable later. Terminal means this playback
attempt ends; pressing Play again can start a new attempt. Repeated starts retain
the failure for the existing 15-second TTL, and successful decisions replace it.
Slow preparation retains the shared background flight and can be joined by a
later request; each media acquisition wait now uses the same 12-second budget.
Normal successful preparation/admission still runs after the bounded precheck.

Real-Debrid code 35 now remains `ErrBlocked`, skips that provider/hash for the
service lifetime (including QueueDownload), does not retry a known torrent via
re-add, and does not trip account backoff. Other accounts/releases remain eligible.
This refusal memory is in-process; it is not persisted across server restarts.

## Reference comparison and limits

This branch's recorded PMS corpus is `testdata/conform/plex` and the comparison
package is `internal/conform` (import alias `pmsconform`). Its decision recordings
cover successful Web/iOS/Apple TV cases, not unavailable-file or failed-transcoder
responses. We did not invent failure captures or claim an exact recorded match.

Additional recorded PMS decision-refusal and session-gate facts were checked.
case and its handlers remain unchanged.

[Plex's published PMS API](https://developer.plex.tv/pms/) documents 1xxx–4xxx
decision classes and start.* failures 400/403/404. Its examples do not establish
which particular status a missing file or failed encoder uses in every PMS
version. [Plex's playback integration guide](https://catalogs-docs.plex.tv/avod-direct-playback-partner-guide/)
also specifies timeline termination text, although that guide targets partner
players and is not proof of any official app's UI behavior.

HTTP tests cannot prove that proprietary clients show custom text or cease
buffering. All physical-device verification, including Fire Cube, remains
unverified. The server defects and its terminal contracts are covered offline.

## Gates

- Package-only `go test ./internal/plex ./internal/debrid`: debrid passed;
  Plex initially failed old assertions expecting retryable 503. Updated those
  assertions and reran the affected cases.
- `TestPlaybackFailureClientContract`: nine client labels, fourteen failure
  classes, XML/JSON decisions, repeated HLS/DASH/part fallback and requests that
  skip decision; terminal status, specific text, no Retry-After, bounded prepare
  interface and completion within the decision budget. This tests server contracts,
  not proprietary client behavior or a live encoder subprocess.
- Targeted Plex gates include the updated uncached matrix, source/version
  sequence, direct-play range, DASH starts/failures, unavailable-title discovery,
  failure retention and successful decision conformance against recorded PMS.
- Targeted debrid gates cover blocked releases, next-account failover and no
  download retry after rejection. Two refused hashes do not prevent a third
  playable release; a refused hash is still skipped a day later.
- `git diff --check`. No full repository, race, web, build or live-device suite.

## GitHub reply draft (not posted)

Thanks — the spinning circle exposed two server bugs, so our earlier conclusion
that this was just the Fire TV app's choice was premature.

For v0.3.5 we fixed a failure that was lost before the playback session existed,
and stopped returning retry advice for completed failed playback attempts. Starts
and direct-play reads now end with a terminal error, and the specific reason is
available in both the decision and buffering timeline response. Source acquisition
for clients that skip decision also uses the bounded wait.

We also fixed Real-Debrid's `infringing_file` handling: a blocked release is
skipped without pausing the whole account, so Ether can try the next release or
provider. It is not re-added during that server run.

Offline tests cover the HTTP contracts used by Fire TV/Android TV/Shield,
Apple TV/iOS, Plex Web, LG/Samsung and Roku. We haven't verified the dialog on a
physical Fire Cube, so we can't yet promise that app displays the custom text.
Please retest on v0.3.5; if it still buffers, a Fire TV client log alongside the
Ether log will let us trace the remaining client behavior.
