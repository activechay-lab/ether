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
	if a.deployment != nil && len(server.NodeRoles) == 1 && server.NodeRoles[0] == config.RoleTranscode {
		return server.ValidateNodeSettings(database)
	}
	return validateNodeSettings(server, database)
}
