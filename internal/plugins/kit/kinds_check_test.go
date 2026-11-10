package kit

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/braedonsaunders/ether/internal/plugins"
)

func kitKind() plugins.MediaKindMeta {
	return plugins.MediaKindMeta{
		Key:     "recording",
		Label:   "Field recordings",
		Library: plugins.KindLibraryDecl{Name: "Field recordings"},
		Levels: []plugins.KindLevel{
			{Key: "album", Label: "Album"},
			{Key: "track", Label: "Track", Playable: true},
		},
		Present: plugins.Presentations{MediaBrowser: &plugins.MediaBrowserPresentation{CollectionType: "music"}},
		Sync:    plugins.KindSyncDecl{Mode: plugins.KindSyncPush},
	}
}

func kitContentManifest(kind plugins.MediaKindMeta) []byte {
	man := map[string]any{
		"id": "community.example.library", "version": "1.0.0", "name": "Library",
		"ether": map[string]any{
			"protocol": 1, "kind": "content", "summary": "Adds a field recordings library",
			"runtime":      map[string]any{"kind": "remote"},
			"capabilities": []string{"catalog_provider", "kind_resolver"},
			"contributes":  map[string]any{"mediaKinds": []any{kind}},
		},
	}
	raw, _ := json.Marshal(man)
	return raw
}

// The kit and host validation must agree field for field (lint parity,
// spec §12 slice B acceptance).
func TestKitKindsLintParity(t *testing.T) {
	ctx := context.Background()
	rep := Check(ctx, &Package{ManifestRaw: kitContentManifest(kitKind())})
	if !rep.OK {
		t.Fatalf("valid content manifest: %+v", rep.Errors)
	}

	bad := kitKind()
	bad.Levels[1].Playable = false
	raw := kitContentManifest(bad)
	host := CheckInstall(ctx, raw, nil)
	rep = Check(ctx, &Package{ManifestRaw: raw})
	if rep.OK || host == nil {
		t.Fatalf("invalid kind must fail in kit and host: ok=%v host=%v", rep.OK, host)
	}
	hostFields := map[string]bool{}
	for _, f := range host.(*plugins.ValidationError).Fields {
		hostFields[f.Field] = true
	}
	for _, e := range rep.Errors {
		if e.Rule == "manifest" && !hostFields[e.Field] {
			t.Fatalf("kit reports %s but host does not (%v)", e.Field, hostFields)
		}
	}
}

func TestValidateKindPage(t *testing.T) {
	decl := kitKind()
	page := plugins.KindPage{Items: []plugins.KindItem{
		{ID: "trk-1", Level: "track", ParentID: "alb-1", Title: "Dawn chorus", Playable: true},
		{ID: "trk-1", Level: "track", ParentID: "alb-1", Title: "Dawn chorus again", Playable: true},
		{ID: "bad id", Level: "side", Title: "Nope"},
	}}
	issues := ValidateKindPage(decl, page, 2)
	if len(issues) < 3 {
		t.Fatalf("want page-size, duplicate and envelope issues: %+v", issues)
	}
	good := plugins.KindPage{Items: page.Items[:1]}
	if issues := ValidateKindPage(decl, good, 10); len(issues) != 0 {
		t.Fatalf("valid page: %+v", issues)
	}
}

func TestKitCheckKindUpgrade(t *testing.T) {
	parse := func(raw []byte) plugins.Manifest {
		m, err := plugins.ParseManifest(raw)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	prev := parse(kitContentManifest(kitKind()))
	shrunk := kitKind()
	shrunk.Levels = shrunk.Levels[:1]
	if issues := CheckKindUpgrade(prev, parse(kitContentManifest(shrunk))); len(issues) == 0 {
		t.Fatal("removing a level must be reported")
	}
	grown := kitKind()
	grown.Levels = append(grown.Levels, plugins.KindLevel{Key: "take", Label: "Take", Playable: true})
	if issues := CheckKindUpgrade(prev, parse(kitContentManifest(grown))); len(issues) != 0 {
		t.Fatalf("additive upgrade: %+v", issues)
	}
}
