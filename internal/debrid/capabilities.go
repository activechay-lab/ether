package debrid

import "context"

// knownTorrentFinder is a package-specific optional capability.
type knownTorrentFinder interface{ findKnown(string) string }

// backgroundTorrentFinder never waits for a network listing. A cold list
// starts one shared read-only refresh, retaining the background budget.
type backgroundTorrentFinder interface {
	findBackground(context.Context, string) string
}

// backgroundCacheOnlyProvider is a provider whose Acquire, with
// CachedOnly set and no torrent id, serves already-cached content
// without adding anything to the account (Premiumize's directdl).
// Only such providers may proceed in a background check after a
// positive CheckCached: every other provider's id-less Acquire adds
// a torrent, which background work must never do.
type backgroundCacheOnlyProvider interface{ supportsBackgroundCacheOnly() bool }

// edgePortableLinks is a provider whose CDN serves any of its links from
// every edge: a link's path and token stand on hosts other than the one
// the API pinned (measured on TorBox during its 2026-11-09 maintenance
// window). Only such providers may replay a link whose pinned edge died on
// an edge the engine remembers (resumeOnAnotherEdge).
type edgePortableLinks interface{ edgePortableLinks() bool }

// cdnEdgeApprover reports whether candidate (a host:port remembered from a
// completed block) stands in the same CDN as the host serving raw. It is
// the engine's last check before a probe hands the link's signed token to
// that edge: a provider without an approver never replays, whatever its
// other capabilities.
type cdnEdgeApprover interface {
	approvesCDNEdge(raw, candidate string) bool
}
