package core

import "context"

type localResponseKey struct{}

// WithLocalResponse requests an immediate answer from local snapshots and
// caches. Optional enrichment may refresh behind the response.
func WithLocalResponse(ctx context.Context) context.Context {
	return context.WithValue(ctx, localResponseKey{}, true)
}

// LocalResponse reports whether this read must avoid enrichment waits.
func LocalResponse(ctx context.Context) bool {
	v, _ := ctx.Value(localResponseKey{}).(bool)
	return v
}
