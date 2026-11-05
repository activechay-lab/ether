package core

import "context"

// Indexer purposes say why an indexer API request was made. The owner
// console breaks each indexer's real API hits and grabs down by purpose, so
// an owner comparing Ether with the indexer's own counter can see what
// spent the quota. Speculative work (SpeculativeIndexerWork: warm-ups,
// detail discovery, availability) shares a small daily slice of each
// indexer's limit and searches narrowly, so work nobody is waiting on
// leaves the rest to playback.
const (
	IndexerPurposeFeed         = "feed"         // release-feed polls
	IndexerPurposeAvailability = "availability" // background availability checks
	IndexerPurposePlayback     = "playback"     // a viewer pressed Play (incl. failover)
	IndexerPurposeDetail       = "detail"       // a viewer opened a playable detail page
	IndexerPurposeWarmup       = "warmup"       // warm-ups, prefetch, managed cache, watchlist
	IndexerPurposeRescue       = "rescue"       // second-pass rescue searches
	IndexerPurposeCaps         = "caps"         // capability (t=caps) fetches
	IndexerPurposeTest         = "test"         // the owner's "Test" button
	IndexerPurposeConsole      = "console"      // owner release searches and checks in the console
	IndexerPurposeOther        = "other"        // untagged callers
)

type indexerPurposeKey struct{}

// WithIndexerPurpose tags ctx's indexer requests with purpose. The
// innermost tag wins, so a rescue fan-out started by playback counts as
// rescue; speculative work is never re-tagged out of its class.
func WithIndexerPurpose(ctx context.Context, purpose string) context.Context {
	if purpose == "" {
		return ctx
	}
	return context.WithValue(ctx, indexerPurposeKey{}, purpose)
}

// IndexerPurpose returns ctx's purpose tag, or IndexerPurposeOther.
func IndexerPurpose(ctx context.Context) string {
	if p, ok := ctx.Value(indexerPurposeKey{}).(string); ok && p != "" {
		return p
	}
	return IndexerPurposeOther
}

// InheritIndexerPurpose carries src's purpose tag into dst, for work
// detached from the caller's cancellation (a search that keeps filling the
// cache after Play started).
func InheritIndexerPurpose(dst, src context.Context) context.Context {
	if p, ok := src.Value(indexerPurposeKey{}).(string); ok && p != "" {
		return context.WithValue(dst, indexerPurposeKey{}, p)
	}
	return dst
}

// SpeculativeIndexerWork reports whether ctx's indexer requests are work no
// viewer pressed Play on: a warm-up not yet promoted to Play, a preload,
// prefetch, watchlist or cache-fill search (WithUnattendedSearch), a
// detail-page version discovery, or a background availability check. Such
// work shares a small slice of each indexer's daily limit, asks only the
// primary query and never fans out into rescue searches. Play and the
// owner's own console actions are not speculative.
func SpeculativeIndexerWork(ctx context.Context) bool {
	if IsUnattendedSearch(ctx) {
		return true
	}
	switch IndexerPurpose(ctx) {
	case IndexerPurposeDetail, IndexerPurposeAvailability:
		return true
	}
	return false
}
