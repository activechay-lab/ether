package frontend

import (
	"context"
	"time"
)

type synchronousArtworkMetadataKey struct{}

// WithSynchronousArtworkMetadata lets a caller wait briefly for hashing that
// is already in flight. Stored rows are joined without this budget. The page
// cache clears it before filling a page, so a browse never holds the cache
// open for a hash.
func WithSynchronousArtworkMetadata(ctx context.Context, budget time.Duration) context.Context {
	if ctx == nil || budget <= 0 {
		return ctx
	}
	return context.WithValue(ctx, synchronousArtworkMetadataKey{}, time.Now().Add(budget))
}

// WithoutSynchronousArtworkMetadata drops an in-flight artwork wait so a
// caller, such as the browse page cache, returns as soon as stored rows are joined.
func WithoutSynchronousArtworkMetadata(ctx context.Context) context.Context {
	if ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, synchronousArtworkMetadataKey{}, nil)
}

// SynchronousArtworkDeadline is the time after which artwork hashing returns
// to the background worker. The second result is false when no budget was set.
func SynchronousArtworkDeadline(ctx context.Context) (time.Time, bool) {
	if ctx == nil {
		return time.Time{}, false
	}
	deadline, ok := ctx.Value(synchronousArtworkMetadataKey{}).(time.Time)
	return deadline, ok
}

type noArtworkMetadataKey struct{}

// WithoutArtworkMetadata marks a read whose responses never carry image
// dimensions or hashes (an IPTV export): artwork lists skip the per-image
// metadata lookup and do not queue hashing for the titles they list.
func WithoutArtworkMetadata(ctx context.Context) context.Context {
	if ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, noArtworkMetadataKey{}, true)
}

// ArtworkMetadataWanted is false under WithoutArtworkMetadata.
func ArtworkMetadataWanted(ctx context.Context) bool {
	if ctx == nil {
		return true
	}
	skip, _ := ctx.Value(noArtworkMetadataKey{}).(bool)
	return !skip
}

type artworkKindsKey struct{}

// WithArtworkKinds marks a read that signs only these image kinds (a page
// signer's Kinds): artwork lists skip work that only another kind uses, such
// as scheduling a backdrop's UltraBlur colours for a poster-only listing.
func WithArtworkKinds(ctx context.Context, kinds map[string]bool) context.Context {
	if ctx == nil || len(kinds) == 0 {
		return ctx
	}
	return context.WithValue(ctx, artworkKindsKey{}, kinds)
}

// ArtworkKindWanted reports whether kind may be used by this read: always,
// unless WithArtworkKinds narrowed it.
func ArtworkKindWanted(ctx context.Context, kind string) bool {
	if ctx == nil {
		return true
	}
	kinds, ok := ctx.Value(artworkKindsKey{}).(map[string]bool)
	return !ok || kinds[kind]
}
