package plex

import (
	"context"
	"github.com/braedonsaunders/ether/internal/frontend"

	"strconv"
)

// PlexProvider delegates Plex proofs to the existing Plex authenticator,
// which validates membership against the current server.
type PlexProvider struct {
	Auth frontend.IdentityAuthenticator
}

func (PlexProvider) Name() string { return "plex" }
func (p PlexProvider) Verify(ctx context.Context, proof frontend.IdentityProof) (frontend.VerifiedIdentity, error) {
	if p.Auth == nil || frontend.IsGrantCredential(proof.Token) {
		return frontend.VerifiedIdentity{}, frontend.ErrUnauthorized
	}
	user, err := p.Auth.Authenticate(ctx, proof.Token)
	if err != nil {
		return frontend.VerifiedIdentity{}, err
	}
	if user == nil || user.PlexID <= 0 {
		return frontend.VerifiedIdentity{}, frontend.ErrUnauthorized
	}
	return frontend.VerifiedIdentity{ViewerID: user.ID, Provider: p.Name(), Subject: strconv.FormatInt(user.PlexID, 10)}, nil
}

var _ frontend.IdentityProvider = PlexProvider{}
