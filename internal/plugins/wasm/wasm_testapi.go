// These helpers support offline acceptance tests across package boundaries.
// Runtime callers use the production APIs in the other files.
package wasm

import (
	"context"
	"encoding/json"
)

func (f HostFunc) CallHost(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	return f(ctx, name, args)
}
