package app

import (
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestSplitAppOverrides(t *testing.T) {
	in := []core.ClientCapabilityOverride{{Product: "Plex for Android", MaxHeight: 2160}}
	got, count := splitAppOverrides(in)
	if count != 1 || len(got) != 3 || got[0].Product != "Plex for Android" || got[1].Product != "Plex for Android (Mobile)" || got[2].Product != "Plex for Android (TV)" || got[2].CopiedFrom != in[0].Product || got[2].MaxHeight != 2160 {
		t.Fatalf("count=%d got=%+v", count, got)
	}
	_, count = splitAppOverrides(got)
	if count != 0 {
		t.Fatal("migration must be idempotent")
	}
}

func TestExpandedOverridesRemainValidAtOldLimit(t *testing.T) {
	in := make([]core.ClientCapabilityOverride, 128)
	for i := range in {
		in[i] = core.ClientCapabilityOverride{Product: "Plex", MaxHeight: 2160}
	}
	expanded, _ := splitAppOverrides(in)
	cfg := config.Defaults("")
	cfg.Clients.Overrides = expanded
	for _, err := range admin.ValidateConfig(cfg) {
		if strings.HasPrefix(err.Field, "clients.overrides") {
			t.Fatalf("migration rejected: %+v", err)
		}
	}
}
