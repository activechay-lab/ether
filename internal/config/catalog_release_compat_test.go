package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogReleaseMigration(t *testing.T) {
	for _, mode := range []string{ModePlayable, ModeDigital, ModeImmediate} {
		for _, gate := range []bool{false, true} {
			raw, _ := json.Marshal(map[string]any{"catalog": map[string]any{"gateMoviesByHomeRelease": gate, "showUpcomingEpisodes": true}, "availability": map[string]any{"mode": mode}})
			cfg, changed, err := decodeStartupConfig(Defaults(t.TempDir()), raw)
			want := mode
			if gate && mode == ModeImmediate {
				want = ModeDigital
			}
			if err != nil || !changed || cfg.Availability.EffectiveMode() != want || cfg.Catalog.ReleaseMode != want || !cfg.Catalog.ShowUpcomingEpisodes || cfg.Catalog.ShowUpcomingMovies {
				t.Fatalf("migration: %+v %v", cfg, err)
			}
			encoded, _ := json.Marshal(cfg)
			if strings.Contains(string(encoded), "gateMoviesByHomeRelease") {
				t.Fatal("legacy field persisted")
			}
			again, changed, err := decodeStartupConfig(Defaults(t.TempDir()), encoded)
			if err != nil || changed || again.Availability.EffectiveMode() != want {
				t.Fatal("not idempotent")
			}
		}
	}
}

func TestReleaseMigrationPersistsAndDefaultsAreOptIn(t *testing.T) {
	dir := t.TempDir()
	def := Defaults(dir)
	if def.Catalog.ShowUpcomingMovies || def.Catalog.ShowUpcomingEpisodes {
		t.Fatal("upcoming defaults enabled")
	}
	raw := []byte(`{"catalog":{"gateMoviesByHomeRelease":true},"availability":{"disableMovieEvidenceGate":true}}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if store.Get().Availability.EffectiveMode() != ModeDigital {
		t.Fatal("legacy immediate not migrated")
	}
	saved, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "gateMoviesByHomeRelease") {
		t.Fatal("legacy field kept")
	}
	loaded, err := Read(dir)
	if err != nil || loaded.Availability.EffectiveMode() != ModeDigital {
		t.Fatal("migration not persistent", err)
	}
}
