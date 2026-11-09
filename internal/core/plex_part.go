package core

import "context"

type plexPartKey struct{}

// WithPlexPart marks a byte-range request as a Plex part request, which
// selects the PMS byte-serving contract without changing the representation
// semantics of other frontends or the encoder loopback.
func WithPlexPart(ctx context.Context) context.Context {
	return context.WithValue(ctx, plexPartKey{}, true)
}

// IsPlexPart reports whether ctx carries the Plex part marker.
func IsPlexPart(ctx context.Context) bool {
	v, _ := ctx.Value(plexPartKey{}).(bool)
	return v
}
