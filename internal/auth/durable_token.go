package auth

import (
	"context"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// DurableTokenCurrent checks that an enrolled raw credential still belongs to
// the accepted active viewer in the authoritative database. It bypasses local
// token caches so another process's revocation is visible immediately. It does
// not enroll upstream credentials or validate delegations, grants or sessions.
func (s *Service) DurableTokenCurrent(ctx context.Context, token string, viewerID int64) error {
	token = strings.TrimSpace(token)
	if s == nil || s.db == nil || ctx == nil || viewerID <= 0 || token == "" || core.IsGrantCredential(token) || IsOwnerSession(token) || strings.HasPrefix(token, delegationPrefix) {
		return core.ErrUnauthorized
	}
	var current int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM auth_users WHERE id=? AND active=1 AND id=(SELECT user_id FROM auth_tokens WHERE token_hash=?)`, viewerID, HashToken(token)).Scan(&current)
	if err != nil || current != viewerID {
		return core.ErrUnauthorized
	}
	return nil
}
