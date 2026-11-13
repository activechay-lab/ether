package frontend

import "context"

type IdentityProof struct {
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
type IdentityProvider interface {
	Name() string
	Verify(context.Context, IdentityProof) (VerifiedIdentity, error)
}

type IdentityAuthenticator interface {
	Authenticate(context.Context, string) (*User, error)
}
