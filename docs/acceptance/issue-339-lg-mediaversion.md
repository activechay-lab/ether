# LG playback settings identity (#339, v0.3.5)

## Evidence and scope

Issue: https://github.com/braedonsaunders/ether/issues/339 (paustin316-cell).
The supplied screenshot reports a lookup of missing `MediaVersion`, while the
settings dictionary contains Quality, Direct Play/Stream and subtitle settings.
The reporter's Ether and Plex app versions remain unknown. No LG hardware or
reporter support ZIP was available for end-to-end confirmation.

The requested older conformance paths have moved: this release uses
`testdata/conform/plex`, `internal/conform`, `internal/conform/plex`, and
`internal/plex/conform_test.go`. The recorded PMS 1.43.4 decision corpus returns
only the selected Media, with its source identity; start returns a playlist or
bytes, not an item. These are projections/transport responses, not new version
catalogues. Do not expand decisions to multiple Media merely to equalize counts.

Checked `~/Documents/.ether-ref/research` and `rex` for client references:
no checked-out client bundle or `MediaVersion` implementation is present.
rex documents an optional ignored `webclient/` directory, absent here.
The official hosted desktop main bundle (Plex Web 4.163.0) has no literal
`MediaVersion` or the screenshot's error string; the hosted `/tv/` URL is 404.
Thus the exact TV creation/lookup branches cannot be independently established
from this checkout. The supplied shared-client behavior is the working
explanation: version-dependent settings creation followed by a lookup using
state from a different response. No proprietary code or assets were copied.
Indie's pre-load Web settings error is consistent with this class, not proven
to be the identical client failure.

## Reproduction

`TestVersionIdentityAcrossPlayFlow` drives the HTTP handlers for LG, Samsung,
Android TV and Web, with zero, one and three cached variants. It uses an offline
catalogue/resolver/playback fixture, not an account-claimed benchmark server.
On the unmodified release branch all 12 cases fail:

- Cold or single-version hub: `[1000000000000550]`; after refill the pre-play
  response becomes `[1000000000000550, 9002600922596233, 9003529759870400]`.
- Three cached versions: the hub still emits only `[1000000000000550]`, while
  the details page emits three.
- `playIdentities` previously protected lists only after admission. An issued
  singleton was treated as no list when resolving `mediaIndex`, allowing an
  old nonzero preference to select a candidate discovered after that response.
- With no issued list, recomputing a positional choice against current results
  could also identify a different release after restart or idle expiry.

The #267 stream/identity work is present on this base, but did not cover these
pre-admission transitions.

## Change

Hubs, library listings, detail metadata (including checkFiles/includeChapters)
and queues reuse the first ordered version list per viewer/device/title. Both
unresolved Auto and a resolved singleton are authoritative. Cached search
completion, reordering and expiry cannot change that list during the flow.
Existing stable release-derived Media/Part IDs remain in use. Stream rendering
still belongs to detail responses; browsing does not start per-title searches
or variant watchers. A concurrent render reads back the retained winning list.

A positional version choice resolves only against the issued list. Missing or
out-of-range positions use Auto; explicit stable Part IDs still name releases.
The existing one-Part model ignores stale partIndex values. Decisions retain
PMS's single selected Media projection and must name an issued identity. Start
is followed by metadata checks to verify that admission/probing retained the
list. Existing stream/probe/failover and VIDAA behavior remains covered.

Tradeoff: versions discovered after an unresolved singleton was issued appear
in a later flow after the existing 30-minute idle identity TTL, rather than
changing settings beneath an open client. Version notifications can refresh
facts, but do not replace that client's list. Active sessions retain their list.

## Verification

One lasting flow invariant test covers hub → pre-play → queue → decision →
start → metadata, checks ordered Media IDs/count and matching Part IDs, and
verifies selected-release admission and stale choices across the 12 cases.
Existing list, refill and missing-list tests were updated for the new invariant.

Gates: package tests and vet only; no full repository, race or web suites.
The full Plex package run exposed the old expectation that a blocked refill
should expand an already-issued singleton; that assertion now requires the
singleton to remain stable. Final gates passed:

- `CGO_ENABLED=0 go test ./internal/plex -count=1`
- `go test ./internal/conform/plex -count=1`
- `go vet ./internal/plex`
- `git diff --check`

## GitHub reply draft (not posted)

A fix for this version-list inconsistency is prepared for v0.3.5. Ether could
show one version in a row and a different list in the details/play queue, or
change that list when a background search completed. That can invalidate the
LG app's playback settings.

The fix keeps the same version IDs and order throughout the play flow, and
prevents old version preferences from selecting a newly discovered release.
An automated flow test covers unresolved, single-version and multi-version
titles for LG, Samsung, Android TV and Plex Web. PMS playback decisions still
identify only the chosen version, as expected by Plex clients.

Please retry after installing v0.3.5. If the error remains, the Ether version,
LG/webOS and Plex app versions, affected title, and a support ZIP captured
while reproducing it will help confirm what is happening on your TV.
