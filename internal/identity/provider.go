package identity

import (
	"context"

)

// Proof carries provider-specific credentials. Providers interpret only the
// fields they support; future providers can add parameters without changing
// identity_links or grants.
type Proof struct {
	Username, Password, Token string
	Parameters                map[string]string
}

// VerifiedIdentity is proof of a linked identity, not a surface grant.
type VerifiedIdentity struct {
	ViewerID, IdentityID int64
	Provider, Subject    string
}

// Provider verifies a proof and resolves it to a household viewer. Grant
// issuance and surface authorization are deliberately separate.
type Provider interface {
	Name() string
	Verify(context.Context, Proof) (VerifiedIdentity, error)
}

func (*Local) Name() string { return "local" }
func (l *Local) Verify(ctx context.Context, proof Proof) (VerifiedIdentity, error) {
	viewer, id, err := l.Authenticate(ctx, proof.Username, proof.Password)
	if err != nil {
		return VerifiedIdentity{}, err
	}
	return VerifiedIdentity{ViewerID: viewer, IdentityID: id, Provider: l.Name(), Subject: proof.Username}, nil
}

var _ Provider = (*Local)(nil)
