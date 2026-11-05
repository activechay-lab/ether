package store

import (
	"reflect"
	"testing"
)

func TestMigrationDependenciesOrderAndValidate(t *testing.T) {
	regMu.Lock()
	oldMigrations, oldDependencies := migrations, dependencies
	migrations = nil
	dependencies = map[string]map[string]bool{}
	regMu.Unlock()
	t.Cleanup(func() { regMu.Lock(); migrations, dependencies = oldMigrations, oldDependencies; regMu.Unlock() })
	Register("alpha", 2, "SELECT 2")
	Register("alpha", 1, "SELECT 1")
	Register("middle", 1, "SELECT 1")
	Register("zeta", 1, "SELECT 1")
	RegisterAfter("alpha", "zeta")
	var order []string
	for _, m := range registered() {
		order = append(order, m.component)
		if m.component == "alpha" && (len(order) == 3 && m.version != 1 || len(order) == 4 && m.version != 2) {
			t.Fatal("component versions are not ordered")
		}
	}
	if !reflect.DeepEqual(order, []string{"middle", "zeta", "alpha", "alpha"}) {
		t.Fatalf("order %v", order)
	}
	RegisterAfter("zeta", "alpha")
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("cycle was accepted")
			}
		}()
		registered()
	}()
	regMu.Lock()
	delete(dependencies, "zeta")
	regMu.Unlock()
	RegisterAfter("middle", "missing")
	if got := registered(); len(got) != 4 {
		t.Fatalf("partial registry: %d", len(got))
	}
}

// A version has one base migration even when its SQL is empty. Dialect-first
// registration must still work, since components initialize independently.
func TestRegisterRejectsDuplicateBaseMigration(t *testing.T) {
	old := migrations
	migrations = nil
	t.Cleanup(func() { migrations = old })
	for _, text := range []string{"", "SELECT 1"} {
		migrations = nil
		RegisterDialect("probe", 1, Postgres, "SELECT 2")
		Register("probe", 1, text)
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("duplicate base migration accepted")
				}
			}()
			Register("probe", 1, text)
		}()
		if got := migrations[0].sqlFor(Postgres); got != "SELECT 2" {
			t.Fatal(got)
		}
	}
}
