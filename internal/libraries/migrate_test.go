package libraries

import (
	"context"
	"testing"
)

// TestMigrateConfigNames is the §12 slice-A acceptance for the config
// side: a pre-v0.3.8 config document's library names become the
// names of libraries 1/2, exactly once.
func TestMigrateConfigNames(t *testing.T) {
	s, _ := openService(t)
	ctx := context.Background()

	legacy := []byte(`{"server":{"friendlyName":"Ether","moviesLibraryName":"Basement Films","tvShowsLibraryName":"Basement Series"}}`)
	movies, shows, err := LegacyConfigNames(legacy)
	if err != nil || movies != "Basement Films" || shows != "Basement Series" {
		t.Fatalf("LegacyConfigNames = %q, %q, %v", movies, shows, err)
	}
	if err := s.MigrateConfigNames(ctx, movies, shows); err != nil {
		t.Fatal(err)
	}
	libs, err := s.List(ctx)
	if err != nil || libs[0].Name != "Basement Films" || libs[1].Name != "Basement Series" {
		t.Fatalf("after migration = %+v, %v", libs, err)
	}
	// An owner rename after the migration is never reverted by
	// re-running it against the same stale config.
	if err := s.Rename(ctx, 1, "Owner Films"); err != nil {
		t.Fatal(err)
	}
	if err := s.MigrateConfigNames(ctx, movies, shows); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, 1)
	if err != nil || got.Name != "Owner Films" {
		t.Fatalf("owner rename reverted: %+v, %v", got, err)
	}
}

func TestMigrateConfigNamesKeepsDefaults(t *testing.T) {
	s, _ := openService(t)
	ctx := context.Background()
	movies, shows, err := LegacyConfigNames([]byte(`{"server":{}}`))
	if err != nil || movies != "" || shows != "" {
		t.Fatalf("LegacyConfigNames(empty) = %q, %q, %v", movies, shows, err)
	}
	if err := s.MigrateConfigNames(ctx, movies, shows); err != nil {
		t.Fatal(err)
	}
	libs, err := s.List(ctx)
	if err != nil || libs[0].Name != "Movies" || libs[1].Name != "TV Shows" {
		t.Fatalf("defaults lost: %+v, %v", libs, err)
	}
	if _, _, err := LegacyConfigNames([]byte(`{`)); err == nil {
		t.Fatal("malformed config accepted")
	}
}
