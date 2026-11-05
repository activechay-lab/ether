package app

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestSourceRoleConstructionBoundary(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Close()
	db := store.OpenTest(t)
	a := &App{opts: Options{DataDir: dir}, cfg: cfg, db: db, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := a.buildCluster(store.SQLite); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.buildSourceRole(ctx, cfg.Get()); err != nil {
		t.Fatal(err)
	}
	if a.playback == nil || a.resolver == nil || a.fetcher == nil || a.cache == nil || a.users == nil || a.safety == nil {
		t.Fatal("source dependencies missing")
	}
	if a.transcoder != nil || a.frontends != nil || a.registrar != nil || a.gdm != nil || a.personalize != nil || a.handler != nil {
		t.Fatal("source constructed an unrelated role service")
	}
}
