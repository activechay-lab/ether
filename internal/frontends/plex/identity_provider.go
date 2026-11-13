package plex

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/identity"
	"strconv"
)

// PlexProvider delegates Plex proofs to the existing Plex authenticator,
// which validates membership against the current server.
type PlexProvider struct{ Auth identity.Authenticator }

func (PlexProvider) Name() string { return "plex" }
func (p PlexProvider) Verify(ctx context.Context, proof identity.Proof) (identity.VerifiedIdentity, error) {
	if p.Auth == nil || core.IsGrantCredential(proof.Token) {
		return identity.VerifiedIdentity{}, core.ErrUnauthorized
	}
	user, err := p.Auth.Authenticate(ctx, proof.Token)
	if err != nil {
		return identity.VerifiedIdentity{}, err
	}
	if user == nil || user.PlexID <= 0 {
		return identity.VerifiedIdentity{}, core.ErrUnauthorized
	}
	return identity.VerifiedIdentity{ViewerID: user.ID, Provider: p.Name(), Subject: strconv.FormatInt(user.PlexID, 10)}, nil
}

var _ identity.Provider = PlexProvider{}
