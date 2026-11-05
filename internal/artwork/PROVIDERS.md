# Owner-configured poster sources

Disabled by default. `config.Posters` is shared configuration. The current
snapshot is supplied through `ImagesOptions.Posters` in app `collectionOptions`,
so saved settings take effect without restarting.
The existing overlay image source detects `GetPoster` and `PosterVersion`.

Verified primary sources (2026-10-28):

- PostersPlus: https://github.com/UmbraProjects/PostersPlus/blob/dev/URL_REFERENCE.md
  `/poster?tmdb_id=ID&type=movie|tv&access_key=KEY`. Owner supplies the instance
  base URL and optional access key, and configures its style on that instance.
- XRDB: https://github.com/IbbyLabs/XRDB/blob/e6d631650ac7469e4536dc61bb7d762c09140e5e/docs/setup/aiometadata.md
  Supply the hosted or self-hosted instance URL, an optional profile ID or alias, and the
  optional instance `XRDB_API_KEY`. Ether requests `/poster/tmdb:movie:ID` or
  `/poster/tmdb:series:ID` with the profile in `config` and the key in `key`.
  Omit the profile to use the instance default. Configure custom profiles in XRDB first.
- BetterPosters: https://btttr.cc/configure — the page's `updateAioUrl` exports
  `https://btttr.cc/poster/imdb/poster-default/{imdb_id}.jpg` by default, with
  optional path/query style settings. Paste its exported URL for customization.
- RPDB: https://rpdb.docs.apiary.io/ (announces migration to https://rpdb.apidoc.io/)
  `https://api.ratingposterdb.com/KEY/tmdb/poster-default/movie-ID.jpg`, or
  `series-ID.jpg` for shows. Owner supplies a key; account tier limits apply.
- MDBList: https://api.mdblist.com/docs/ documents metadata APIs. No dedicated
  image-rendering endpoint was verified, so no fabricated MDBList image preset
  is shipped. Ether's existing ratings integration remains separate.

Custom HTTP(S) templates support `{imdb_id}`, `{tmdb_id}`, `{type}` (movie/tv),
and `{api_key}`. A TMDB placeholder requires the type placeholder. Missing IDs
skip the provider. Seasons and episodes keep their own TMDB artwork; their IDs
are never sent as movie or show IDs. Templates and keys are sealed and redacted.
Changing destination requires re-entering retained credentials.

Owner-configured LAN hosts are intentional. Hosts are fixed in templates,
userinfo and fragments are forbidden, and all redirects are refused. Requests
have an eight-second deadline, two active fetch/decode slots, an 8 MiB response
limit and a 16-megapixel decode limit (8192 per dimension). Output is a resized
JPEG. Provider errors and bodies are never logged or returned to clients.

Provider originals use the existing bounded memory LRU and durable image disk
cache (512 MiB by default, shared with image variants). The stable key hashes the
resolved provider URL, including profile and credentials; neither raw URLs nor
credentials are stored in cache metadata. Client width and the six-hour client
version are excluded. Originals retain ETag, Last-Modified and freshness metadata
on disk. Freshness defaults to six hours, capped by upstream max-age/Age;
no-cache forces conditional revalidation and no-store prevents persistence.
Expired originals send If-None-Match and If-Modified-Since. A 304 renews freshness
without rendering/downloading another poster. Changed content selects new local
JPEG derivatives, keyed by content digest and bounded width. One detached flight
per original coalesces concurrent widths and survives individual cancellation.
Failed requests fall back to TMDB without poisoning the provider cache.

URL/profile/key edits immediately select a different original. Provider-side
rating/style changes are discovered on expiry; viewer ratings do not change a
provider's generated poster. Existing six-hour presentation versions still
refresh client URLs, but no longer force an unconditional upstream render.
Eviction can require a new fetch; this is a bounded cache, not permanent storage.

Ether overlays composite the resulting image, so disable overlapping badges
in either service. The saved-source artwork preview uses the same pipeline.
Plex thumbnail URLs carry only opaque versions. Provider responses use content
ETags and revalidation to avoid making transient TMDB fallbacks permanent.

Limitations: no live account tests or supplied credentials; no cross-origin or
same-origin redirects; no season/episode-specific provider artwork; no provider
style editor (use provider configurators). Collection mosaics continue to use
TMDB. Plex clients may keep previously downloaded thumbnails until refresh.

Offline replay validation (2026-11-05): a fake XRDB slept three seconds per
request and served two titles. Home requested widths 200 and 400 concurrently;
the library requested width 600. Repeating all sizes and reconstructing the
image store against the same disk directory added no requests. Before: six
upstream requests, 9.062 s cold. After: two requests, 3.044 s cold. This measures
the artwork-fetch replay, not end-to-end client rendering or a live XRDB account.
