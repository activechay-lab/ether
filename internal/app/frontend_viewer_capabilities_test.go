package app

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontends/plex"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/safety"
)

func TestViewerCapabilitiesPolicyAndMembership(t *testing.T) {
	ctx := context.Background()
	a := setupTestApp(t, true)
	local, err := identity.NewLocal(a.db)
	if err != nil {
		t.Fatal(err)
	}
	id, err := local.CreateViewer(ctx, "alice", "password-1234")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.users.RefreshViewers(ctx); err != nil {
		t.Fatal(err)
	}
	if err = a.safety.Put(ctx, id, safety.Policy{RemoteDisabled: true, MaxBitrateKbps: 3000, BlockedKeywords: []string{"violence"}}); err != nil {
		t.Fatal(err)
	}
	alice, err := a.users.ActiveUser(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	alice.MaxContentRating, alice.StreamLimit = "PG", 2
	if err = a.users.UpdateUser(ctx, *alice); err != nil {
		t.Fatal(err)
	}
	experience := frontendtest.Experience{ViewsFunc: func(_ context.Context, v *core.Viewer) ([]frontend.View, error) {
		if v.ID != id {
			t.Fatal("wrong viewer")
		}
		return []frontend.View{{ID: "2", Name: "Films"}}, nil
	}}
	call := a.viewerCapabilities(local, experience)
	viewer := &frontend.Viewer{ID: id}
	lan, err := call(ctx, viewer, frontend.Device{}, netip.MustParseAddr("192.168.1.2"))
	if err != nil {
		t.Fatal(err)
	}
	if lan.IsOwner == nil || *lan.IsOwner || lan.CanPlayRemote == nil || *lan.CanPlayRemote || lan.PeerRemote == nil || *lan.PeerRemote || lan.HasLocalPassword == nil || !*lan.HasLocalPassword || lan.RemoteBitrateKbps == nil || *lan.RemoteBitrateKbps != 3000 || lan.MaxRating == nil || *lan.MaxRating != "PG" || lan.AllowedLibraries == nil || len(*lan.AllowedLibraries) != 1 {
		t.Fatalf("LAN: %+v", lan)
	}
	if lan.CanPlay == nil || !*lan.CanPlay || lan.MaxActiveSessions == nil || *lan.MaxActiveSessions != 2 || lan.BlockedTags == nil || len(*lan.BlockedTags) != 1 || (*lan.BlockedTags)[0] != "violence" {
		t.Fatalf("LAN policy: %+v", lan)
	}
	active, err := a.users.ActiveUser(ctx, id)
	inst, ok := a.frontends.registry.Instance("plex")
	if !ok {
		t.Fatal("Plex not mounted")
	}
	plexServer := inst.Handler().(*plex.Server)
	if err != nil || lan.CanDownload == nil || *lan.CanDownload != plexServer.DownloadsAllowed(active) {
		t.Fatalf("download policy: %+v, %v", lan, err)
	}
	remote, err := call(ctx, viewer, frontend.Device{}, netip.MustParseAddr("8.8.8.8"))
	if err != nil || remote.PeerRemote == nil || !*remote.PeerRemote || remote.CanPlay == nil || *remote.CanPlay {
		t.Fatalf("remote: %+v %v", remote, err)
	}
	if err = a.users.SetOwnerEnabled(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if _, err = call(ctx, viewer, frontend.Device{}, netip.MustParseAddr("192.168.1.2")); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("inactive: %v", err)
	}
}
