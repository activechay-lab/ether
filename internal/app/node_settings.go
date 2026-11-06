package app

import (
	"errors"

	"github.com/braedonsaunders/ether/internal/config"
)

// validateNodeSettings runs before storage and role-dependent components are
// constructed. database must include deployment environment overrides. Replace
// the full-server guard when isolated specialist services and remote execution
// are wired; membership metadata alone does not enable a specialist runtime.
func validateNodeSettings(server config.Server, database config.Database) error {
	if err := server.ValidateNodeSettings(database); err != nil {
		return err
	}
	if !server.NodeRoles.FullServer() {
		return errors.New("specialist runtime is not yet enabled; use the full-server role set or leave node roles unset")
	}
	return nil
}

// Deployment is mandatory for isolated roles; plain config edits never bypass
// the historical fail-closed guard or create an unauthenticated private service.
func (a *App) validateRuntimeSettings(server config.Server, database config.Database) error {
	if a.deployment != nil && specialistSingletonRole(server.NodeRoles) {
		return server.ValidateNodeSettings(database)
	}
	return validateNodeSettings(server, database)
}

// Kubernetes/compose deployments run each specialist as exactly one role
// (server, source, transcode or delivery). Background-only and combined
// subsets have no complete construction/route contract and stay refused.
func specialistSingletonRole(roles config.NodeRoles) bool {
	if len(roles) != 1 {
		return false
	}
	switch roles[0] {
	case config.RoleServer, config.RoleSource, config.RoleTranscode, config.RoleDelivery:
		return true
	default:
		return false
	}
}
