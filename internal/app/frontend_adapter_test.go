package app

import (
	"context"
	"errors"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/libraries"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/frontendadapter"
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

// TestAdapterStreamURLRefusesForeignAndUnknown proves the StreamURL backend
// rejects items that do not belong to the calling plugin and items that are
// not stored, before any ticket is minted.
func TestAdapterStreamURLRefusesForeignAndUnknown(t *testing.T) {
	a := setupTestApp(t, true)
	ctx := context.Background()
	issuer := frontendadapter.NewTicketIssuer()
	fn := a.adapterStreamURL(issuer)
	if fn == nil {
		t.Skip("no plugin runtime in this build")
	}

	// An item ref from another plugin is refused.
	if _, err := fn(ctx, "plug.mine", 7, "pk:other:kind:item-1"); !errors.Is(err, plugins.ErrPermissionDenied) {
		t.Fatalf("foreign item ref: %v", err)
	}
	// A malformed ref is not found.
	if _, err := fn(ctx, "plug.mine", 7, "not-a-ref"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("malformed ref: %v", err)
	}
	// A missing viewer is refused.
	if _, err := fn(ctx, "plug.mine", 0, "pk:plug.mine:kind:item-1"); !errors.Is(err, plugins.ErrPermissionDenied) {
		t.Fatalf("no viewer: %v", err)
	}
}
