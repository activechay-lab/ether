package admin_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestChannelLogoRequiresOwner(t *testing.T) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	live, err := livetv.New(livetv.Options{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	cfg := newStore(t, func(c *config.Config) { seededConfig(c); c.SetupComplete = true })
	h := admin.NewHandler(admin.Deps{Config: cfg, IsOwnerToken: storedOwner(cfg), Extensions: map[string]http.Handler{"livetv": live.Handler()}})
	r := req(http.MethodGet, "/ether/api/livetv/channels/missing/logo", "")
	r.RemoteAddr = "203.0.113.1:1234"
	r.Host = "ether.example"
	if got := serve(h, r).Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d", got)
	}
	r.Header.Set("X-Plex-Token", ownerToken)
	if got := serve(h, r).Code; got != http.StatusNotFound {
		t.Fatalf("owner: %d", got)
	}
}
