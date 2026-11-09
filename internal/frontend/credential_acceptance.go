package frontend

import (
	"context"
	"github.com/braedonsaunders/ether/internal/auth"
)

// CredentialAcceptance is an opaque host receipt. Only the authenticator can
// mint it; formatting remains redacted and current revocation is rechecked.
type CredentialAcceptance = auth.Acceptance
type CredentialAcceptor interface {
	Accept(context.Context, string) (*Viewer, CredentialAcceptance, error)
	ReceiptCurrent(context.Context, CredentialAcceptance) error
}
