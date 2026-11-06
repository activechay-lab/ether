package app

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
)

func TestSpecialistSingletonRolesRequireDeployment(t *testing.T) {
	pg := config.Database{Driver: "pgx"}
	for _, role := range []config.NodeRole{config.RoleServer, config.RoleSource, config.RoleTranscode, config.RoleDelivery} {
		server := config.Server{NodeRoles: config.NodeRoles{role}}
		if err := (&App{}).validateRuntimeSettings(server, pg); err == nil {
			t.Fatalf("plain settings enabled specialist role %s", role)
		}
		a := &App{deployment: &deploymentRuntime{}}
		if err := a.validateRuntimeSettings(server, pg); err != nil {
			t.Fatalf("implemented deployment singleton %s denied: %v", role, err)
		}
		if err := a.validateRuntimeSettings(server, config.Database{}); err == nil {
			t.Fatalf("SQLite enabled specialist role %s", role)
		}
	}
	for _, roles := range []config.NodeRoles{
		{config.RoleBackground}, {config.RoleServer, config.RoleDelivery},
		{config.RoleSource, config.RoleTranscode}, {config.RoleSource, config.RoleSource}, {"unknown"},
	} {
		if err := (&App{deployment: &deploymentRuntime{}}).validateRuntimeSettings(config.Server{NodeRoles: roles}, pg); err == nil {
			t.Fatalf("unsupported role subset enabled: %v", roles)
		}
	}
}
