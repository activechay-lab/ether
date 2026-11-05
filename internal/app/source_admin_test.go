package app

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/sourceadmin"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestSourceAdminOwnerUsesDurableAuthority(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	users, err := auth.New(ctx, db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer users.Close()
	local, err := identity.NewLocal(db)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := local.CreateOwner(ctx, "lab-owner", "lab-only-long-password")
	if err != nil {
		t.Fatal(err)
	}
	if err = users.RefreshViewers(ctx); err != nil {
		t.Fatal(err)
	}
	grants, err := identity.NewGrants(db, users, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := grants.IssueOwnerLogin(ctx, owner, nil, "owner", core.Device{ID: "lab"})
	if err != nil {
		t.Fatal(err)
	}
	a := &App{db: db, users: users}
	if err = a.verifySourceOwner(ctx, token); err != nil {
		t.Fatal(err)
	}
	delegated, err := users.IssueDelegation(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.verifySourceOwner(ctx, delegated); err == nil {
		t.Fatal("delegated credential gained source administration")
	}
	// Leave the process viewer cache intact: every private operation must still
	// observe the shared durable owner revocation before touching a provider.
	if _, err = db.ExecContext(ctx, `UPDATE auth_users SET is_owner=0 WHERE id=?`, owner); err != nil {
		t.Fatal(err)
	}
	if err = a.verifySourceOwner(ctx, token); err == nil {
		t.Fatal("cached owner assertion survived durable revocation")
	}
	if err = a.verifySourceOwner(ctx, ""); err != sourceadmin.ErrDenied {
		t.Fatalf("tokenless setup admitted: %v", err)
	}
}

func TestSourceAdminSameOwnerTokenRotation(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	primary, err := auth.New(ctx, db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()
	account := plextv.Account{ID: 142, Username: "lab-owner"}
	if err = primary.SetOwner(ctx, account, "old-lab-token"); err != nil {
		t.Fatal(err)
	}
	source, err := auth.New(ctx, db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	a := &App{db: db, users: source}
	if err = a.verifySourceOwner(ctx, "old-lab-token"); err != nil {
		t.Fatal(err)
	}
	if err = primary.SetOwner(ctx, account, "new-lab-token"); err != nil {
		t.Fatal(err)
	}
	// The source process cache intentionally has not refreshed. Raw-token
	// authority must follow the durable credential row, not the unchanged owner.
	if err = a.verifySourceOwner(ctx, "old-lab-token"); err == nil {
		t.Fatal("revoked cached owner credential authorized a private operation")
	}
	if err = a.verifySourceOwner(ctx, "new-lab-token"); err != nil {
		t.Fatalf("current durable owner credential denied: %v", err)
	}
}
