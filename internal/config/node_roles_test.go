package config

import (
	"encoding/json"
	"testing"
)

func TestNodeRolesDefaultsAndValidation(t *testing.T) {
	for _, roles := range []NodeRoles{nil, {}, {RoleServer, RoleSource, RoleTranscode, RoleDelivery, RoleBackground}} {
		if !roles.FullServer() || (Server{NodeRoles: roles}).ValidateNodeSettings(Database{}) != nil {
			t.Fatal("full server defaults changed")
		}
	}
	for _, roles := range []NodeRoles{{RoleServer}, {RoleTranscode, RoleBackground}, {RoleDelivery}, {RoleSource}} {
		s := Server{NodeRoles: roles}
		if s.ValidateNodeSettings(Database{}) == nil {
			t.Fatal("specialist accepted SQLite")
		}
		if err := s.ValidateNodeSettings(Database{Driver: DatabasePostgres}); err != nil {
			t.Fatal(err)
		}
	}
	for _, roles := range []NodeRoles{{"unknown"}, {RoleServer, RoleServer}, {"Server"}, {""}} {
		if roles.Validate() == nil {
			t.Fatalf("invalid roles accepted: %v", roles)
		}
	}
	if (NodeRoles{}).Has("unknown") {
		t.Fatal("unknown capability implicitly enabled")
	}
	b, err := json.Marshal(Server{})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["nodeRoles"] != nil || fields["internalAddress"] != nil {
		t.Fatal("new settings change zero JSON")
	}
}

func TestInternalAddressValidation(t *testing.T) {
	for _, address := range []string{"", "https://worker.internal:8443", "https://[::1]:8443/"} {
		if err := ValidateInternalAddress(address); err != nil {
			t.Fatal(err)
		}
	}
	for _, address := range []string{"http://worker:8443", "https://secret:password@worker", "https://worker/path", "https://worker?secret=value", "https://worker#fragment", "worker:8443", "https://worker:", "https://worker:65536", "https://worker:0", "https://worker:bad", "https://[invalid]", "https://::1"} {
		if ValidateInternalAddress(address) == nil {
			t.Fatal("malformed endpoint accepted")
		}
	}
}
