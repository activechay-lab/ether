package identity

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/core"
)

// Authenticator validates an external credential for its bound surface.
type Authenticator interface {
	Authenticate(context.Context, string) (*core.User, error)
}

// Resolver selects validators supplied by application wiring. External tokens
// never fall back to grant validation; grants never reach external providers.
type Resolver struct {
	validators map[string]Authenticator
	grants     *Grants
}

func NewResolver(validators map[string]Authenticator, grants *Grants) (*Resolver, error) {
	if grants == nil {
		return nil, errors.New("identity: grants required")
	}
	bound := make(map[string]Authenticator, len(validators))
	for surface, validator := range validators {
		if surface == "" || validator == nil {
			return nil, errors.New("identity: invalid credential validator")
		}
		bound[surface] = validator
	}
	return &Resolver{validators: bound, grants: grants}, nil
}
func (r *Resolver) Resolve(ctx context.Context, surface, credential string) (*core.Viewer, error) {
	if validator := r.validators[surface]; validator != nil {
		if IsGrantCredential(credential) {
			return nil, core.ErrUnauthorized
		}
		return validator.Authenticate(ctx, credential)
	}
	return r.grants.Resolve(ctx, surface, credential)
}

var _ core.Identity = (*Resolver)(nil)
