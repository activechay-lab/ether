package identity_test

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestWebCodeLifecycle(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if _, err := db.Exec(`INSERT INTO auth_users(id,plex_id,created_at,updated_at) VALUES(1,123,0,0),(2,456,0,0)`); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(10000, 0)
	g, err := identity.NewGrants(db, members{db}, time.Hour, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	code, err := g.IssueWebCode(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	token, id, err := g.RedeemWebCode(ctx, code, "0123456789abcdef", "")
	if err != nil || id != 1 {
		t.Fatalf("redeem: id=%d err=%v", id, err)
	}
	v, err := g.Resolve(ctx, "jellyfin", token)
	if err != nil || v.ID != 1 {
		t.Fatalf("binding: %v", err)
	}
	if _, _, err = g.RedeemWebCode(ctx, code, "0123456789abcdef", ""); err == nil {
		t.Fatal("code reused")
	}
	code, err = g.IssueWebCode(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	token2, id, err := g.RedeemWebCode(ctx, code, "0123456789abcdef", token)
	if err != nil || id != 2 || token2 == token {
		t.Fatalf("cross-user reuse: %v", err)
	}
	code, err = g.IssueWebCode(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, _, err = g.RedeemWebCode(ctx, code, "0123456789abcdef", ""); err == nil {
		t.Fatal("expired code accepted")
	}
}
