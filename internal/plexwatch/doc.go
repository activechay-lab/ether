// Package plexwatch syncs one viewer's watched marks and ratings with that
// viewer's own Plex account, and (pms*.go) with Plex Media Servers the owner
// runs.
//
// # Account sync
//
// Transport: discover.provider.plex.tv, the calls Plex Web makes for "Mark as
// watched" on Discover, verified against a real account on 2026-11-07:
//
//   - GET /library/metadata/{k1,k2,...}/userState answers MediaContainer.
//     UserState (an object for one key, an array for several) with ratingKey,
//     type, viewCount, viewOffset, lastViewedAt (unix seconds), userRating
//     (1–10) and, for shows, viewedLeafCount. Mixed movie/show/episode batches
//     work; reads are capped at maxStateBatch keys.
//   - PUT /actions/scrobble, /actions/unscrobble and /actions/rate (rating=1–10,
//     -1 clears), each with identifier=tv.plex.provider.discover&key={key}.
//     They are idempotent. Unscrobble resets viewCount but keeps lastViewedAt,
//     so "watched" is viewCount>0, never lastViewedAt alone.
//
// {key} is the hex id inside a title's verified plex://movie|show|episode GUID
// (internal/plexguid). Titles without a verified GUID are never synced and no
// other identity matching is attempted.
//
// Credential: the viewer's own Plex account token from the PIN sign-in
// (POST/GET plex.tv/api/v2/pins), stored sealed per viewer and sent only in
// X-Plex-Token. The PIN must be approved by the Plex account already recorded
// for that viewer; the owner's token is never used for a viewer. Approving the
// PIN is the viewer's consent; the account's "Sync My Watch State and Ratings"
// setting governs Plex Media Servers and is not consulted. A 401 or 403 erases
// the token and marks the connection for reconnecting.
//
// Ether → Plex: watch changes write durable intents to state_sync_journal in
// the watch transaction, stamped with the viewer's fence generation. The
// sender claims a row, resolves the GUID, and admits it under the fence lock
// (generation unchanged, sending still on) before the request leaves.
// Turning sending off, disconnecting and reconnecting move the fence under
// the same lock, so nothing is admitted afterwards; a request admitted just
// before cannot be recalled. Admission sets never_sent=0. When an attempt
// ends without a definitive answer (5xx, timeout, crash), the next attempt
// reads userState first: if Plex already holds the change it is done, if
// Plex shows a newer change it yields, otherwise it is sent again. 429 pauses
// the viewer for Retry-After; other failures back off exponentially.
//
// Plex → Ether: a leader-only, bounded background pass per viewer reads
// userState in batches over Ether's verified movie and show identities,
// resuming from a key cursor. Shows whose watched-episode count or last view
// moved queue their episodes for reading. plexwatch_remote keeps what was
// last read per title, so only changes since then apply: a Plex view by its
// lastViewedAt against the local watch clock; an unwatch or rating change,
// which Plex does not timestamp, only when Ether has not changed that title
// since the previous read. Imported changes are account events, which are
// never journaled, and values Ether wrote itself are adopted rather than
// applied, so nothing echoes in either direction.
//
// Resume positions (viewOffset) are not synced.
package plexwatch
