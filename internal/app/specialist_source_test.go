package app

import (
	"context"
	"io"
	"log/slog"
	"os"
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
	// Construction resolves an executable without starting it. Media/process
	// acceptance belongs to the source-prober invariants and shipping lab.
	probePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sourceCfg := cfg.Get()
	sourceCfg.Transcode.FFprobePath = probePath
	if err := a.buildSourceRole(ctx, sourceCfg); err != nil {
		t.Fatal(err)
	}
	if a.sourceProber == nil || a.playback == nil || a.resolver == nil || a.fetcher == nil || a.cache == nil || a.users == nil || a.safety == nil {
		t.Fatal("source dependencies missing")
	}
	if err := cfg.Update(func(c *config.Config) error { c.Cache.BudgetBytes = 1234567; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, budget := a.cache.Usage(); budget != 1234567 {
		t.Fatal("source-owned configuration did not apply")
	}
	if a.transcoder != nil || a.frontends != nil || a.registrar != nil || a.gdm != nil || a.personalize != nil || a.handler != nil {
		t.Fatal("source constructed an unrelated role service")
	}
}
