package app

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/libraries"
)

// TestAdapterCatalogResolvesKindLibraries proves an adapter's declared media
// kind keys resolve to the library ids those kinds created, and that a key
// with no library yet is skipped rather than guessed.
func TestAdapterCatalogResolvesKindLibraries(t *testing.T) {
	a := setupTestApp(t, true)
	ctx := context.Background()
	if a.libraries == nil {
		t.Skip("no libraries service in this build")
	}
	created, err := a.libraries.UpsertPluginLibrary(ctx, "plug.records", "recording", libraries.PluginLibraryDecl{Name: "Field recordings"})
	if err != nil {
		t.Fatal(err)
	}
	cat := adapterCatalog{app: a}
	ids := cat.resolveLibraries(ctx, "plug.records", []string{"recording"})
	if len(ids) != 1 || ids[0] != created.ID {
		t.Fatalf("resolved %v, want [%d]", ids, created.ID)
	}
	// Unknown kind key contributes nothing.
	if ids := cat.resolveLibraries(ctx, "plug.records", []string{"nope"}); len(ids) != 0 {
		t.Fatalf("unknown kind key resolved to %v", ids)
	}
	// A different plugin's library is not reachable by key alone.
	if ids := cat.resolveLibraries(ctx, "other.plugin", []string{"recording"}); len(ids) != 0 {
		t.Fatalf("cross-plugin key resolved to %v", ids)
	}
	if got := core.PluginLibraryKey("plug.records", "recording"); got != created.Key {
		t.Fatalf("library key = %q, want %q", got, created.Key)
	}
}
