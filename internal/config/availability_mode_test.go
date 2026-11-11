package config

import "testing"

func TestAvailabilityModeFor(t *testing.T) {
	base := Availability{Mode: ModePlayable}
	if got := base.ModeFor("movie"); got != ModePlayable {
		t.Fatalf("movie fallback = %q, want %q", got, ModePlayable)
	}
	if got := base.ModeFor("show"); got != ModePlayable {
		t.Fatalf("show fallback = %q, want %q", got, ModePlayable)
	}

	split := Availability{
		Mode:      ModePlayable,
		MovieMode: ModeDigital,
		ShowMode:  ModeImmediate,
	}
	if got := split.ModeFor("movie"); got != ModeDigital {
		t.Fatalf("movie explicit = %q, want %q", got, ModeDigital)
	}
	for _, k := range []string{"show", "season", "episode", ""} {
		if got := split.ModeFor(k); got != ModeImmediate {
			t.Fatalf("ModeFor(%q) = %q, want %q", k, got, ModeImmediate)
		}
	}

	var cfg Config
	cfg.Availability = split
	syncDerived(&cfg)
	if cfg.Catalog.ReleaseMode != ModeDigital {
		t.Fatalf("syncDerived Catalog.ReleaseMode = %q, want %q", cfg.Catalog.ReleaseMode, ModeDigital)
	}
}
