package core

import "context"

type browseScopeKey struct{}

// WithBrowseScope identifies a tokenless client's section/sort/filter sequence.
// Protocol adapters omit offsets and page sizes, and never include credentials.
func WithBrowseScope(ctx context.Context, scope string) context.Context {
	return context.WithValue(ctx, browseScopeKey{}, scope)
}

func BrowseScope(ctx context.Context) string {
	scope, _ := ctx.Value(browseScopeKey{}).(string)
	return scope
}
