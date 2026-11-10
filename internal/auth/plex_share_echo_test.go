package auth

import (
	"context"
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plextv"
)

// Saving a viewer from its effective record (a language change, or the
// console editing the stream limit) must not store the plex.tv share as an
// Ether restriction: libraries later shared on app.plex.tv must still apply.
func TestUpdateUserDoesNotFreezePlexShare(t *testing.T) {
	ctx := context.Background()
	s, err := New(ctx, openDBAt(t, t.TempDir()+"/ether.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetOwner(ctx, plextv.Account{ID: 42, Username: "owner"}, "server-token"); err != nil {
		t.Fatal(err)
	}
	share := func(sections ...string) {
		t.Helper()
		if err := s.SyncFriends(ctx, []plextv.Friend{{PlexID: 100, Username: "alice", Token: "alice-access", Sections: sections}}); err != nil {
			t.Fatal(err)
		}
	}
	share("1", "2")
	alice, err := s.Authenticate(ctx, "alice-access")
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.ActiveUser(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(current.AllowedLibraries, []core.LibraryID{1, 2}) {
		t.Fatalf("effective sections %v, want the plex.tv share", current.AllowedLibraries)
	}
	next := *current
	next.AudioLanguages = []string{"nl"}
	if err := s.UpdateUser(ctx, next); err != nil {
		t.Fatal(err)
	}
	share("1", "2", "3") // the owner shares another library on app.plex.tv
	got, err := s.ActiveUser(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.AllowedLibraries, []core.LibraryID{1, 2, 3}) || !slices.Equal(got.AudioLanguages, []string{"nl"}) {
		t.Fatalf("after save and reshare: sections %v languages %v", got.AllowedLibraries, got.AudioLanguages)
	}
	// An explicit Ether restriction is still stored.
	got.AllowedLibraries = []core.LibraryID{2}
	if err := s.UpdateUser(ctx, *got); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.ActiveUser(ctx, alice.ID); !slices.Equal(got.AllowedLibraries, []core.LibraryID{2}) {
		t.Fatalf("explicit restriction lost: %v", got.AllowedLibraries)
	}
}
