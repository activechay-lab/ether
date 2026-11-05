// These helpers support offline acceptance tests across package boundaries.
// Runtime callers use the production APIs in the other files.
package capability

import (
	"context"
	"database/sql"
)

// BindHeadersInTx inserts headers on the caller's transaction. The caller owns
// scope authorization, per-domain quota, timestamp selection and atomic domain
// insertion; tx must come from the same database as the domain operation.
// Duplicate namespace/digest fails and must cause the caller to roll back.
func BindHeadersInTx(ctx context.Context, tx *sql.Tx, headers []Header) error {
	if tx == nil || len(headers) == 0 || len(headers) > HeaderBatchMax {
		return ErrInvalidHeader
	}
	for _, h := range headers {
		if !validHeaderScope(h.HeaderScope) || !digestValid(h.Digest) || h.ExpiresMillis <= 0 {
			return ErrInvalidHeader
		}
	}
	return insertHeadersInTx(ctx, tx, headers)
}
