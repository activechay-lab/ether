package identity

import (
	"context"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// Proof carries provider-specific credentials. Providers interpret only the
// fields they support; future providers can add parameters without changing
// identity_links or grants.
type Proof = frontend.IdentityProof
type VerifiedIdentity = frontend.VerifiedIdentity
type Provider = frontend.IdentityProvider

func (*Local) Name() string { return "local" }
func (l *Local) Verify(ctx context.Context, proof Proof) (VerifiedIdentity, error) {
	viewer, id, err := l.Authenticate(ctx, proof.Username, proof.Password)
	if err != nil {
		return VerifiedIdentity{}, err
	}
	return VerifiedIdentity{ViewerID: viewer, IdentityID: id, Provider: l.Name(), Subject: proof.Username}, nil
}

var _ Provider = (*Local)(nil)
