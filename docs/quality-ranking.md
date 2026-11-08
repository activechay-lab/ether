# Quality ranking: the Radarr/Sonarr standard, streamed

Status: design for v0.3.6, implemented on `feat/036-arr-standard-ranking`
on top of evulhotdog's PR #355 (per-profile Servarr evaluation).

## Principle

Radarr and Sonarr decide what to **download and keep**. Ether decides what
to **stream to this device right now**. Ether uses their ranking standard
as is (one quality order, custom formats with scores, the same comparer).
Ether adds only what a download manager can't know: the device, the
viewer, the connection and whether a source plays now. That context is a
set of filters and gates in front of the standard order, plus a few
built-in custom formats that score like any other format. It is not a
second ranking ladder.

There is one model for every profile. The per-profile "Ranking: Like
Radarr/Sonarr | Ether" switch and the "What matters most" ladder are
removed. A TRaSH Guides or Profilarr import is a configuration pack (a
quality order, custom formats, scores and a minimum score), and it ranks
the way its authors wrote it.

## The comparer

Releases that pass every filter are sorted by the first key that differs:

| # | Key | From | Notes |
| --- | --- | --- | --- |
| G1 | Only-play fallback tier | Ether rule | An "Only play … otherwise" rule's fallback releases come after its primary ones. |
| G2 | Audio language is acceptable | Ether, viewer | A release that declares only languages nobody asked for comes last. |
| G3 | Fits the viewer's connection | Ether, session | A release the connection can't carry comes after one it can. |
| S1 | Quality: position in the profile's quality order | Radarr/Sonarr `CompareQuality` | Qualities in one group tie. Ether grades the quality first (below). |
| S2 | Revision (Repack/Proper) | `CompareQuality` | Only when the profile scores no Repack/Proper format: Radarr's "Prefer and upgrade". A profile that scores one (TRaSH, and Ether's defaults) gets "Do not prefer", as TRaSH instructs. |
| S3 | Custom format score | `CompareCustomFormatScore` | Sum of every matching format's score on this profile. |
| S4 | Preferred protocol | `CompareProtocol` | The owner's source preference (Usenet or debrid). "Automatic" has none. |
| S5 | Season pack, episode count, first episode | Sonarr only | Season packs first; fewer episodes (more for anime); lower first episode. |
| S6 | Indexer priority | `CompareIndexerPriority` | The indexer's Priority setting, lower first. |
| S7 | Seeders, then peers (both torrents) or age (both Usenet) | `ComparePeersIfTorrent`, `CompareAgeIfUsenet` | Rounded log10 of seeders/peers; Usenet age buckets (< 1 h, ≤ 24 h, ≤ 7 days, then −round(log10 days)). |
| S8 | Size | `CompareSize` | Larger first, in 200 MB steps. Ether has no quality definitions with a preferred size yet, so this is Radarr's no-preferred-size rule. |
| T1 | Played fine recently | Ether | A release that streamed without trouble breaks a full tie. |
| T2 | Indexer reliability | Ether | Learned grab success rate of the indexer. |
| T3 | Starts fastest | Ether routing | Among equivalent releases, a cached debrid copy or the source with the faster measured startup goes first (`routing.go`). Quality is never traded for speed. |

S1–S8 follow `DownloadDecisionComparer` in Radarr (`develop`): quality,
custom format score, protocol, indexer priority, indexer flags, peers if
torrent, age if Usenet, size. Sonarr inserts episode count and episode
number after protocol and has no indexer flags. Ether has no indexer flags
(Radarr compares them only with "Prefer indexer flags" on) and no delay
profiles; the owner's source preference stands in for the delay profile's
preferred protocol. Concepts only: no Radarr/Sonarr code is copied.

### Grading quality

A quality name is a claim. Ether grades it before S1, for every profile:

- A release whose average bitrate is below the floor for its resolution and
  source (a "2160p" Blu-ray encode at 5 Mbps) is graded one resolution
  lower.
- A presumed upscale (named as one, or a 2160p file of a title whose only
  disc release is 1080p) is graded one resolution lower.
- A graded release keeps no HDR or Dolby Vision credit from Ether's
  built-in formats.

Radarr does the same kind of correction from media info after import;
Ether has to do it from the search result.

### Minimum score and "upgrade until"

The **minimum custom format score** works as in Radarr/Sonarr: a release
whose score is strictly below it is not played. It counts the custom
formats you created or imported. Ether's built-in formats, Prefer rules and
plugin points rank releases but don't count toward the minimum, so a
TRaSH minimum of 0 means what its authors meant, and a device that needs
an audio transcode can't push an otherwise wanted release below it.

**Upgrade until** (cutoff quality and cutoff score) and the minimum upgrade
increment are about replacing a file you already keep. A stream has no
file to upgrade, so they don't apply and imports don't carry them.

## Filters, gates and tie-breaks

"Stream now" decides where each piece of context goes. A **filter** removes
a release that can't or mustn't play here. A **gate** orders before quality
because getting it wrong ruins this viewing whatever the quality. A
**format** scores a preference that should trade off against other
preferences. A **tie-break** only decides between otherwise equal releases.

| Context | Placement | Why |
| --- | --- | --- |
| Device can't decode the video codec, or Dolby Vision without an HDR10 base on a non-DV device | Filter (unchanged) | It doesn't play. |
| Above the device's or profile's resolution, outside the size range, CAM when not allowed, outside an imported profile's allowed qualities | Filter (unchanged) | The owner's limits. |
| Never-play and Only-play rules | Filter (unchanged) | The owner's limits. |
| Below the minimum custom format score | Filter | Radarr/Sonarr rule. |
| Recently failed health check, marked bad, blocked, already tried | Filter (unchanged) | Not available now. |
| Unavailable when checked (expired articles, uncached torrent in cached-only mode) | Filter at validation | Ranked validation checks candidates in order and plays the first that is available, so an unavailable release never reaches the viewer. Ranking ahead of quality by *known* availability would make every unchecked 4K lose to a cached 1080p. |
| Would need a video transcode (legacy codec), audio transcode, or HDR tone mapping | Streaming format (penalty) | It still starts and plays. A gate would drop a 4K film to 1080p because the TV can't take TrueHD. Penalties keep today's behaviour: quality first, then the release that plays directly. |
| HDR and Dolby Vision | Built-in format that counts only where the device and profile show it | On an SDR device HDR is a cost, not a gain. |
| Audio language acceptable to the viewer | Gate G2 | A film in a language the viewer doesn't speak is unwatchable; quality can't make up for it. |
| Declares the viewer's language on a foreign title (an English or MULTi release of a Spanish film for an English viewer) | Streaming format | A preference: many viewers want the original with subtitles, so it can't override resolution. |
| Fits the connection | Gate G3 | A release the connection can't carry stalls or forces a transcode down. |
| Series year differs but names no other show | Streaming format (penalty) | Probably right, so it stays playable, but a correctly labelled release of the same quality goes first. |
| Usenet post older than the provider's retention | Streaming format (penalty) | Likely expired; validation would catch it, at a cost in start time. |
| Played fine recently | Tie-break T1 | Evidence about the past, not about this release's quality. |
| Known cached on debrid, measured startup | Tie-break T3 | Starts faster; never worth a lower quality. |

## Built-in custom formats

Ether ships these formats. They are evaluated from Ether's parsed release
and the session, and appear in every profile's format list with a score,
like the formats you import. Scores are per profile; 0 means off.

Release formats (preferences; the defaults below are the 4K profile's):

| Format | Matches | 4K default |
| --- | --- | --- |
| Remux, Blu-ray, WEB-DL, WEBRip, HDTV, DVD | The release type | 8000, 6000, 4000, 2000, 3, 3 |
| Dolby Vision | Dolby Vision the device and profile show | 500 |
| HDR | HDR10, Dolby Vision or a UHD remux the device and profile show | 500 |
| HDR10 | An HDR10 layer the device and profile show | 8 |
| HEVC, AV1, H.264 (also VP9, VC-1, MPEG-2, XviD) | The video codec, when the device decodes it | 18, 12, 6 |
| TrueHD, E-AC-3, DTS-HD, AC-3, AAC (also DTS, FLAC, Opus, MP3) | The audio format | 20, 16, 12, 8, 4 |
| Atmos | Dolby Atmos | 3 |
| Repack/Proper | A Repack or Proper | 6 |
| Low bitrate | Below the bitrate floor for its resolution | −60 |
| Preferred language, Preferred language (other region), Not a preferred language, Preferred subtitles | The profile's Languages | 5, 2, −25, 2 when Languages are set |

Streaming formats (context Ether knows and Radarr doesn't):

| Format | Matches | Default |
| --- | --- | --- |
| Viewer's language | Foreign title; release declares the viewer's audio language or MULTi | 20000 |
| Series year differs | Accepted although the series year differs | −10000 |
| Video transcode | The device can't decode a legacy codec | −20 |
| Audio plays directly | The device plays the audio format | 10 |
| Audio transcode | The device can't play the audio format | −15 |
| Atmos passthrough | Atmos in a format the device plays | 5 |
| Tone mapping | HDR10 on a device or profile without HDR | −15 |
| Past retention | Usenet post older than provider retention | −20 |

The device formats keep their defaults on imported profiles too: device fit
is Ether's job, and at these sizes it only separates releases a pack scores
alike. Viewer's language is 0 on imports, because language preference is
the pack's to express.

Prefer rules are formats too: each Prefer rule has a score. Plugin ranking
steps add the number the plugin returns for a release to its custom format
score.

## Default profiles

The shipped 4K, 1080p and 720p profiles are standard profiles: a quality
order with one group per resolution, highest first, inside the profile's
resolution range, and the built-in format scores above. Grouping by
resolution and scoring release type as a format is how TRaSH's merged
"WEB 1080p" and SQP profiles work; it is what lets the viewer's language
rank above release type within a resolution, as it does today.

Scores are strictly lexicographic: each level is worth more than every
level below it can add up to (rounded up to 1, 2 or 5 × 10ⁿ). In the 4K
profile everything below release type adds up to at most 1213, so WEB-DL
(4000) always beats WEBRip (2000), and HDR (500) only decides between
releases of the same type.

## Conversion of existing profiles

Each stored profile is converted once when the server starts (and in the
cluster's shared settings), deterministically:

1. **Gates.** Audio language and connection fit become gates G2/G3.
   "Played fine recently" becomes tie-break T1.
2. **Quality order.** If the ladder (ignoring gates, Prefer rules and
   plugins) starts with Resolution, the order is one group per resolution.
   If it starts with Release type then Resolution, the order is
   source-major: Remux-2160p, Remux-1080p, … in the profile's source
   order, then one group per resolution for the remaining sources.
3. **Formats.** Every remaining step becomes built-in formats, in ladder
   order, with lexicographic weights computed from what the levels below
   can add (the old total score is the bottom level: codec, audio, Atmos,
   HDR10, compatibility, languages and your own custom format scores at
   face value). The old score's age, grabs, size and bitrate points map
   to the standard keys S7–S8; indexer reputation to T2.
4. **Scores.** Formats whose score was inherited from the global score
   list or a format's default score are written onto the profile, because
   a profile's score list is now complete, as in Radarr/Sonarr. The global
   score list and global minimum move onto each profile and are removed.
5. **Removed fields.** `ranking`, `rankingConfig` (→ `pluginScores`),
   `evaluationMode`, `preferredSources`, `preferredCodecs`,
   `preferredAudio`, `preferLargest`.

Cases the standard order can't express are converted to the nearest
equivalent and logged at startup, per profile:

- Prefer rules ranked above everything; they now rank within a quality.
- A step moved above Resolution (other than Release type) now ranks
  within a quality.
- Audio language or Connection fit moved down now always rank first.
- Played fine recently moved up is now a final tie-break.
- Larger file (or "prefer larger files") now compares at S8.
- A plugin step now adds its points to the custom format score.

### Proof

`internal/resolver/ranking_corpus_test.go` ranks a deterministic corpus
(four titles, 160 release pools, four devices, seven stored profile
shapes: the three shipped profiles, a French-language 4K, a source-first
ladder, a codec/audio-first ladder and a profile with owner custom
formats) and compares it with the picks recorded from the old ranking
before any code changed (`testdata/ranking_corpus_golden.json`). Results
are in the "Conversion results" section below.

## Coming from Radarr, Sonarr or TRaSH

- **Quality profiles** are the same idea: an ordered list of qualities
  (with groups), custom formats with scores, a minimum score. Import yours
  from TRaSH Guides, Profilarr, or paste the JSON.
- **Ordering** is Radarr/Sonarr's: quality, then custom format score, then
  protocol, indexer priority, seeders or age, size.
- **What Ether adds:** it won't play something the device can't decode,
  that the connection can't carry, or in a language the viewer doesn't
  accept; it grades starved encodes and upscales one resolution down; and a
  few streaming formats (audio transcode, tone mapping, …) separate
  releases your pack scores alike. Set any of them to 0.
- **What doesn't apply:** upgrade until, cutoff score and the upgrade
  increment (nothing is kept to upgrade), delay profiles and indexer flags.
- **Propers and repacks:** a profile that scores a Repack/Proper format
  (TRaSH's do) works as "Do not prefer"; one that doesn't works as "Prefer
  and upgrade".
- **Language:** Radarr's profile language becomes the profile's Languages
  plus the viewer's own audio languages; languages are never a hard
  filter except for releases dubbed into a language nobody asked for.
