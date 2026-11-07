package admin

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func mediaConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Defaults(t.TempDir())
	cfg.Quality = config.Quality{
		Profiles: []core.QualityProfile{
			{Name: "Movies", MediaType: core.MediaMovie},
			{Name: "Shows", MediaType: core.MediaTV},
			// One name may serve both media as two profiles.
			{Name: "Any", MediaType: core.MediaMovie},
			{Name: "Any", MediaType: core.MediaTV},
		},
	}
	return cfg
}

func hasField(errs []FieldError, field string) bool {
	for _, e := range errs {
		if e.Field == field {
			return true
		}
	}
	return false
}

func TestValidateConfigMediaScopes(t *testing.T) {
	cfg := mediaConfig(t)
	cfg.Quality.DefaultMovie = "Movies"
	cfg.Quality.DefaultTV = "Shows"
	cfg.Quality.RemoteMovie = "Any"
	cfg.Quality.RemoteTV = "Any"
	cfg.Quality.Default = "Movies"
	cfg.Quality.Remote = "Shows"
	cfg.Quality.Rules = []config.QualityRule{
		{MediaType: core.MediaMovie, Profile: "Movies"},
		{MediaType: core.MediaTV, Profile: "Shows"},
		{Profile: "Any"},
	}
	cfg.Quality.Sync = config.QualitySync{Enabled: true, Interval: config.Duration(24 * time.Hour)}
	if errs := ValidateConfig(cfg); len(errs) != 0 {
		t.Fatalf("valid media config rejected: %v", errs)
	}

	// Legacy defaults may name a media-scoped profile; they stay valid.
	cfg.Quality.Default = "Shows"
	cfg.Quality.Remote = "Movies"
	if errs := ValidateConfig(cfg); len(errs) != 0 {
		t.Fatalf("legacy defaults rejected: %v", errs)
	}

	tests := []struct {
		name   string
		mutate func(*config.Config)
		field  string
	}{
		{
			name:   "profile media type",
			mutate: func(c *config.Config) { c.Quality.Profiles[0].MediaType = "film" },
			field:  "quality.profiles[0].mediaType",
		},
		{
			name:   "profile without a media type",
			mutate: func(c *config.Config) { c.Quality.Profiles[0].MediaType = "" },
			field:  "quality.profiles[0].mediaType",
		},
		{
			name:   "duplicate name within one media",
			mutate: func(c *config.Config) { c.Quality.Profiles[3].Name = "Shows" },
			field:  "quality.profiles[3].name",
		},
		{
			name: "rule media type",
			mutate: func(c *config.Config) {
				c.Quality.Rules = []config.QualityRule{{MediaType: "shows", Profile: "Movies"}}
			},
			field: "quality.rules[0].mediaType",
		},
		{
			name:   "movie default names tv profile",
			mutate: func(c *config.Config) { c.Quality.DefaultMovie = "Shows" },
			field:  "quality.defaultMovie",
		},
		{
			name:   "tv default names movie profile",
			mutate: func(c *config.Config) { c.Quality.DefaultTV = "Movies" },
			field:  "quality.defaultTv",
		},
		{
			name:   "movie remote names tv profile",
			mutate: func(c *config.Config) { c.Quality.RemoteMovie = "Shows" },
			field:  "quality.remoteMovie",
		},
		{
			name:   "tv remote names movie profile",
			mutate: func(c *config.Config) { c.Quality.RemoteTV = "Movies" },
			field:  "quality.remoteTv",
		},
		{
			name:   "movie default names missing profile",
			mutate: func(c *config.Config) { c.Quality.DefaultMovie = "Missing" },
			field:  "quality.defaultMovie",
		},
		{
			name: "media default requires a configured profile",
			mutate: func(c *config.Config) {
				c.Quality.Profiles = nil
				c.Quality.DefaultMovie = "Missing"
			},
			field: "quality.defaultMovie",
		},
		{
			name:   "sync interval zero while enabled",
			mutate: func(c *config.Config) { c.Quality.Sync = config.QualitySync{Enabled: true} },
			field:  "quality.sync.interval",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := mediaConfig(t)
			tc.mutate(&cfg)
			errs := ValidateConfig(cfg)
			if !hasField(errs, tc.field) {
				t.Fatalf("expected %s error, got %v", tc.field, errs)
			}
		})
	}
}

func TestValidateConfigSyncIntervalDisabled(t *testing.T) {
	cfg := mediaConfig(t)
	cfg.Quality.Sync = config.QualitySync{Enabled: false}
	if errs := ValidateConfig(cfg); len(errs) != 0 {
		t.Fatalf("disabled sync with zero interval rejected: %v", errs)
	}
}
