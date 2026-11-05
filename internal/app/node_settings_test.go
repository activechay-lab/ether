package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/cluster"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestNodeSettingsValidation(t *testing.T) {
	full := config.NodeRoles{config.RoleServer, config.RoleSource, config.RoleTranscode, config.RoleDelivery, config.RoleBackground}
	for _, tc := range []struct {
		name     string
		server   config.Server
		database config.Database
		env      string
		want     string
	}{
		{name: "default SQLite"},
		{name: "default PostgreSQL override", env: "postgres://localhost/ether"},
		{name: "explicit full server", server: config.Server{NodeRoles: full}},
		{name: "unknown role", server: config.Server{NodeRoles: config.NodeRoles{"unknown"}}, want: "unknown role"},
		{name: "duplicate role", server: config.Server{NodeRoles: config.NodeRoles{config.RoleServer, config.RoleServer}}, want: "duplicate role"},
		{name: "invalid address", server: config.Server{InternalAddress: "https://owner:secret@private"}, want: "HTTPS origin"},
		{name: "specialist SQLite", server: config.Server{NodeRoles: config.NodeRoles{config.RoleTranscode}}, want: "require PostgreSQL"},
		{name: "specialist effective PostgreSQL", server: config.Server{NodeRoles: config.NodeRoles{config.RoleTranscode}}, env: "postgres://localhost/ether", want: "specialist runtime is not yet enabled"},
		{name: "specialist saved PostgreSQL", server: config.Server{NodeRoles: config.NodeRoles{config.RoleServer}}, database: config.Database{Driver: "pgx"}, want: "specialist runtime is not yet enabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			effective := tc.database.Effective(func(key string) string {
				if key == config.DatabaseURLEnv {
					return tc.env
				}
				return ""
			})
			err := validateNodeSettings(tc.server, effective)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("error disclosed credentials")
			}
		})
	}
}

func TestNodeSettingsRejectBeforeWork(t *testing.T) {
	for _, tc := range []struct {
		name          string
		roles         config.NodeRoles
		address, want string
	}{
		{"specialist", config.NodeRoles{config.RoleTranscode}, "https://worker.internal:8443", "specialist runtime is not yet enabled"},
		{"invalid roles", config.NodeRoles{"unknown"}, "", "unknown role"},
		{"invalid address", nil, "http://worker.internal", "HTTPS origin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(config.DatabaseURLEnv, "postgres://127.0.0.1:1/ether?sslmode=disable")
			t.Setenv("ETHER_PORT", "")
			dir := t.TempDir()
			cfg, err := config.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			scratch, cache := filepath.Join(dir, "scratch-must-not-exist"), filepath.Join(dir, "cache-must-not-exist")
			if err := cfg.Update(func(c *config.Config) error {
				c.Server.NodeRoles, c.Server.InternalAddress = tc.roles, tc.address
				c.Transcode.TempDir, c.Cache.Dir = scratch, cache
				c.Cache.Storage = "local"
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			a, err := New(ctx, Options{DataDir: dir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			if a != nil {
				a.Close()
				t.Fatal("constructed unsupported app")
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			for _, path := range []string{scratch, cache, filepath.Join(dir, cluster.NodeFile), filepath.Join(dir, "ether.db")} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("work started at %s: %v", path, err)
				}
			}
		})
	}
}

func TestNodeSettingsOverlayAndClusterSnapshot(t *testing.T) {
	cfg, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	roles := config.NodeRoles{config.RoleServer, config.RoleSource, config.RoleTranscode, config.RoleDelivery, config.RoleBackground}
	const address = "https://node.internal:8443"
	if err := cfg.Update(func(c *config.Config) error {
		c.Server.NodeRoles = roles
		c.Server.InternalAddress = address
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Even an old/malformed shared document must not override node-local roles.
	merged, err := config.MergeShared(cfg.Get(), []byte(`{"server":{"nodeRoles":["transcode"],"internalAddress":"http://bad"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(merged.Server.NodeRoles, roles) || merged.Server.InternalAddress != address {
		t.Fatal("shared overlay replaced node metadata")
	}
	if err := validateNodeSettings(merged.Server, merged.Database.Effective(nil)); err != nil {
		t.Fatal(err)
	}
	a := &App{cfg: cfg, opts: Options{DataDir: filepath.Dir(cfg.Path())}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := a.buildCluster(store.SQLite); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := cfg.Update(func(c *config.Config) error {
		c.Server.NodeRoles = config.NodeRoles{config.RoleTranscode}
		c.Server.InternalAddress = "https://changed.internal"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	self := a.cluster.Self()
	if !slices.Equal(self.Caps.Roles, roles) || self.InternalAddress != address || self.ProtocolVersion != 0 {
		t.Fatalf("cluster metadata changed or enabled remote runtime: %+v", self)
	}
}
