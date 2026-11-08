// Package reqmemo retains the shared request-memo API. Host readers and
// protocol adapters use the same context key and bounded memo instance.
package reqmemo

import (
	"context"
	kit "github.com/braedonsaunders/ether/internal/frontend/kit/reqmemo"
)

func With(ctx context.Context) context.Context { return kit.With(ctx) }
func Active(ctx context.Context) bool          { return kit.Active(ctx) }
func Do[T any](ctx context.Context, key string, load func() (T, error)) (T, error) {
	return kit.Do(ctx, key, load)
}
func Forget(ctx context.Context, key string)          { kit.Forget(ctx, key) }
func ForgetPrefix(ctx context.Context, prefix string) { kit.ForgetPrefix(ctx, prefix) }
