package auth

import (
	"context"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// DurableOwnerToken authenticates a currently enrolled raw owner credential in
// one authoritative database read. It deliberately bypasses positive/stale token
// caches: privileged private operations must observe same-owner credential
// rotation even before a membership-bus cache invalidation reaches this process.
// Browser identity grants and owner sessions use their own durable verifiers.
// Fresh upstream tokens not enrolled in auth_tokens must first be exchanged for
// an owner session; this method never makes network calls or changes acceptance.
func (s *Service) DurableOwnerToken(ctx context.Context, token string) (*core.User, error) {
	token = strings.TrimSpace(token)
	if s == nil || s.db == nil || token == "" || core.IsGrantCredential(token) || IsOwnerSession(token) {
		return nil, core.ErrUnauthorized
	}
	row, err := scanRow(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM auth_users WHERE active=1 AND is_owner=1 AND id=(SELECT user_id FROM auth_tokens WHERE token_hash=?)`, HashToken(token)))
	if err != nil {
		return nil, core.ErrUnauthorized
	}
	user := row.effective()
	if !user.IsOwner || user.Delegated {
		return nil, core.ErrUnauthorized
	}
	return &user, nil
}
