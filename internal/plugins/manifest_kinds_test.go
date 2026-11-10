package plugins

import (
	"strings"
	"testing"
)

func contentManifest() Manifest {
	m := baseManifest()
	m.ID = "community.example.library"
	m.Name = "Library"
	m.Ether.Kind = KindContent
	m.Ether.Summary = "Adds a field recordings library"
	m.Ether.Capabilities = []Capability{CapCatalogProvider, CapKindResolver, CapMatcher}
	m.Ether.Permissions.Sources = "search"
	m.Ether.Settings = []byte(`{"fields":[{"key":"api_key","kind":"secret","label":"Access key"},{"key":"endpoint","kind":"url","label":"Endpoint"}]}`)
	m.Ether.Contributes.MediaKinds = []MediaKindMeta{validMediaKind()}
	m.Ether.Contributes.Setup = &SetupMeta{Steps: []SetupStep{
		{Key: "connect", Label: "Connect the account", Fields: []string{"api_key", "endpoint"}, Test: "/ether/v1/setup/connect/test", Required: true},
	}}
	return m
}

func validMediaKind() MediaKindMeta {
	return MediaKindMeta{
		Key:     "recording",
		Label:   "Field recordings",
		Library: KindLibraryDecl{Name: "Field recordings"},
		Levels: []KindLevel{
			{Key: "collection", Label: "Collection"},
			{Key: "album", Label: "Album", Container: true},
			{Key: "track", Label: "Track", Playable: true},
		},
		Fields: []KindField{
			{Key: "genre", Type: "enum", Label: "Genre", Indexed: true, Enum: []string{"nature", "city"}},
			{Key: "recordist", Type: "people", Label: "Recordist"},
			{Key: "duration", Type: "duration", Label: "Duration"},
		},
		Art: []KindArt{{Role: "waveform", Label: "Waveform"}},
		Present: Presentations{
			Plex:         &PlexPresentation{Base: "show"},
			MediaBrowser: &MediaBrowserPresentation{CollectionType: "music", ItemTypes: map[string]string{"album": "MusicAlbum", "track": "Audio"}},
		},
		Browse: KindBrowseDecl{Sorts: []string{"title", "added"}, Facets: []string{"genre"}, Search: true, SearchFields: []string{"recordist"}},
		Sync:   KindSyncDecl{Mode: KindSyncPull, Refresh: "1h"},
		Match:  KindMatchDecl{ThresholdDefault: 0.8},
	}
}

func TestContentManifestValidates(t *testing.T) {
	if err := contentManifest().Validate(); err != nil {
		t.Fatalf("valid content manifest: %v", err)
	}
	// Adapter-only presentation with the adapter declared.
	m := contentManifest()
	m.Ether.Capabilities = append(m.Ether.Capabilities, CapFrontendAdapter)
	m.Ether.Contributes.FrontendAdapters = []FrontendAdapterMeta{
		{Key: "feed", Label: "Recordings feed", ProtocolHint: "custom", PathPrefix: "/recordings"},
	}
	m.Ether.Contributes.MediaKinds[0].Present = Presentations{AdapterOnly: true}
	if err := m.Validate(); err != nil {
		t.Fatalf("adapter only kind: %v", err)
	}
	// Resolver-only content with no sync is valid but owns no sync.
	m = contentManifest()
	m.Ether.Capabilities = []Capability{CapKindResolver}
	m.Ether.Contributes.MediaKinds[0].Sync = KindSyncDecl{}
	if err := m.Validate(); err != nil {
		t.Fatalf("resolver only kind: %v", err)
	}
}

func TestContentManifestRules(t *testing.T) {
	kind := func(m *Manifest) *MediaKindMeta { return &m.Ether.Contributes.MediaKinds[0] }
	cases := []struct {
		name  string
		mut   func(*Manifest)
		field string
	}{
		{"content without provider or resolver", func(m *Manifest) {
			m.Ether.Capabilities = []Capability{CapMatcher}
		}, "ether.capabilities"},
		{"provider without kinds", func(m *Manifest) {
			m.Ether.Contributes.MediaKinds = nil
		}, "ether.contributes.mediaKinds"},
		{"provider on extension kind", func(m *Manifest) {
			m.Ether.Kind = KindExtension
		}, "ether.capabilities[0]"},
		{"frontend adapter on source kind", func(m *Manifest) {
			m.Ether.Kind = KindSource
			m.Ether.Capabilities = []Capability{CapFrontendAdapter}
			m.Ether.Contributes.MediaKinds = nil
		}, "ether.capabilities[0]"},
		{"bad kind key", func(m *Manifest) { kind(m).Key = "Recordings" }, ".key"},
		{"duplicate kind key", func(m *Manifest) {
			m.Ether.Contributes.MediaKinds = append(m.Ether.Contributes.MediaKinds, validMediaKind())
		}, "mediaKinds[1].key"},
		{"no levels", func(m *Manifest) { kind(m).Levels = nil }, ".levels"},
		{"six levels", func(m *Manifest) {
			for i := 0; i < 4; i++ {
				kind(m).Levels = append(kind(m).Levels, KindLevel{Key: "extra", Label: "Extra"})
			}
		}, ".levels"},
		{"duplicate level", func(m *Manifest) { kind(m).Levels[1].Key = "collection" }, "levels[1].key"},
		{"no playable level", func(m *Manifest) { kind(m).Levels[2].Playable = false }, ".levels"},
		{"field envelope collision", func(m *Manifest) { kind(m).Fields[0].Key = "title" }, "fields[0].key"},
		{"field bad type", func(m *Manifest) { kind(m).Fields[0].Type = "blob" }, "fields[0].type"},
		{"enum without values", func(m *Manifest) { kind(m).Fields[0].Enum = nil }, "fields[0].enum"},
		{"enum values on text field", func(m *Manifest) {
			kind(m).Fields[1].Enum = []string{"x"}
		}, "fields[1].enum"},
		{"facet not indexed", func(m *Manifest) { kind(m).Fields[0].Indexed = false }, "browse.facets[0]"},
		{"facet unknown field", func(m *Manifest) { kind(m).Browse.Facets = []string{"mood"} }, "browse.facets[0]"},
		{"bad sort", func(m *Manifest) { kind(m).Browse.Sorts = []string{"vibes"} }, "browse.sorts[0]"},
		{"search field not text", func(m *Manifest) {
			kind(m).Browse.SearchFields = []string{"duration"}
		}, "browse.searchFields[0]"},
		{"search fields without search", func(m *Manifest) { kind(m).Browse.Search = false }, "browse.search"},
		{"builtin art role", func(m *Manifest) { kind(m).Art[0].Role = "poster" }, "art[0].role"},
		{"no presentation", func(m *Manifest) { kind(m).Present = Presentations{} }, ".present"},
		{"adapter only without adapter", func(m *Manifest) {
			kind(m).Present = Presentations{AdapterOnly: true}
		}, ".present.adapterOnly"},
		{"plex show one level", func(m *Manifest) {
			kind(m).Levels = []KindLevel{{Key: "track", Label: "Track", Playable: true}}
			kind(m).Present.MediaBrowser = nil
		}, "present.plex.base"},
		{"plex bad base", func(m *Manifest) { kind(m).Present.Plex.Base = "book" }, "present.plex.base"},
		{"mediabrowser bad collection", func(m *Manifest) {
			kind(m).Present.MediaBrowser.CollectionType = "podcasts"
		}, "mediaBrowser.collectionType"},
		{"mediabrowser unknown level", func(m *Manifest) {
			kind(m).Present.MediaBrowser.ItemTypes = map[string]string{"disc": "Audio"}
		}, "mediaBrowser.itemTypes"},
		{"ondemand sync", func(m *Manifest) {
			kind(m).Sync = KindSyncDecl{Mode: "ondemand"}
		}, "sync.mode"},
		{"pull refresh too fast", func(m *Manifest) { kind(m).Sync.Refresh = "5m" }, "sync.refresh"},
		{"push refresh", func(m *Manifest) {
			kind(m).Sync = KindSyncDecl{Mode: KindSyncPush, Refresh: "1h"}
		}, "sync.refresh"},
		{"sync without provider", func(m *Manifest) {
			m.Ether.Capabilities = []Capability{CapKindResolver}
		}, "sync.mode"},
		{"negative cache", func(m *Manifest) { kind(m).Cache.MaxBytes = -1 }, "cache.maxBytes"},
		{"bad cache ttl", func(m *Manifest) { kind(m).Cache.TTL = "soon" }, "cache.ttl"},
		{"threshold over one", func(m *Manifest) { kind(m).Match.ThresholdDefault = 1.5 }, "match.thresholdDefault"},
		{"sources without resolver", func(m *Manifest) {
			m.Ether.Capabilities = []Capability{CapCatalogProvider}
		}, "ether.permissions.sources"},
		{"bad sources level", func(m *Manifest) { m.Ether.Permissions.Sources = "all" }, "ether.permissions.sources"},
		{"adapter without declaration", func(m *Manifest) {
			m.Ether.Capabilities = append(m.Ether.Capabilities, CapFrontendAdapter)
		}, "ether.contributes.frontendAdapters"},
		{"adapter bad prefix", func(m *Manifest) {
			m.Ether.Capabilities = append(m.Ether.Capabilities, CapFrontendAdapter)
			m.Ether.Contributes.FrontendAdapters = []FrontendAdapterMeta{{Key: "feed", Label: "Recordings feed", ProtocolHint: "custom", PathPrefix: "recordings"}}
		}, "frontendAdapters[0].pathPrefix"},
		{"setup field not in schema", func(m *Manifest) {
			m.Ether.Contributes.Setup.Steps[0].Fields = []string{"password"}
		}, "setup.steps[0].fields[0]"},
		{"setup bad test route", func(m *Manifest) {
			m.Ether.Contributes.Setup.Steps[0].Test = "ether/v1/setup/connect/test"
		}, "setup.steps[0].test"},
		{"setup duplicate step", func(m *Manifest) {
			m.Ether.Contributes.Setup.Steps = append(m.Ether.Contributes.Setup.Steps, m.Ether.Contributes.Setup.Steps[0])
		}, "setup.steps[1].key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := contentManifest()
			tc.mut(&m)
			err := m.Validate()
			if err == nil {
				t.Fatalf("want error on %s, got none", tc.field)
			}
			found := false
			for _, f := range err.(*ValidationError).Fields {
				if strings.Contains(f.Field, tc.field) {
					found = true
				}
			}
			if !found {
				t.Fatalf("want field containing %s in %v", tc.field, err.(*ValidationError).Fields)
			}
		})
	}
}

func TestMediaKindDeclHash(t *testing.T) {
	a, b := validMediaKind(), validMediaKind()
	if a.DeclHash() != b.DeclHash() || a.DeclHash() == "" {
		t.Fatal("equal declarations must hash equal")
	}
	b.Label = "Other recordings"
	if a.DeclHash() == b.DeclHash() {
		t.Fatal("changed declaration must hash differently")
	}
}

func TestValidateMediaKindsUpgrade(t *testing.T) {
	base := []MediaKindMeta{validMediaKind()}
	clone := func() []MediaKindMeta {
		k := validMediaKind()
		return []MediaKindMeta{k}
	}
	// Additive: new field, trailing level, enum value, label change.
	next := clone()
	next[0].Label = "Field recordings and more"
	next[0].Fields = append(next[0].Fields, KindField{Key: "mood", Type: "string", Label: "Mood"})
	next[0].Fields[0].Enum = append(next[0].Fields[0].Enum, "night")
	next[0].Levels = append(next[0].Levels, KindLevel{Key: "take", Label: "Take", Playable: true})
	if err := ValidateMediaKindsUpgrade(base, next); err != nil {
		t.Fatalf("additive upgrade: %v", err)
	}
	bad := []struct {
		name string
		mut  func(*MediaKindMeta)
	}{
		{"field removed", func(k *MediaKindMeta) { k.Fields = k.Fields[1:] }},
		{"field retyped", func(k *MediaKindMeta) { k.Fields[0].Type = "string" }},
		{"enum shrunk", func(k *MediaKindMeta) { k.Fields[0].Enum = []string{"nature"} }},
		{"index dropped", func(k *MediaKindMeta) { k.Fields[0].Indexed = false }},
		{"level removed", func(k *MediaKindMeta) { k.Levels = k.Levels[:2] }},
		{"level reordered", func(k *MediaKindMeta) { k.Levels[0], k.Levels[1] = k.Levels[1], k.Levels[0] }},
		{"level reflagged", func(k *MediaKindMeta) { k.Levels[2].Playable = false }},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			n := clone()
			tc.mut(&n[0])
			if err := ValidateMediaKindsUpgrade(base, n); err == nil {
				t.Fatal("want a non-additive upgrade error")
			}
		})
	}
	if err := ValidateMediaKindsUpgrade(base, nil); err == nil {
		t.Fatal("removing every kind must be refused")
	}
}

func TestContentPermissionSheet(t *testing.T) {
	m := contentManifest()
	lines, _, _ := PermissionSheet(m)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Searches your indexers") || !strings.Contains(joined, "Adds a library: Field recordings") {
		t.Fatalf("sheet lines: %v", lines)
	}
	cur := baseManifest()
	if diff := PermissionDiff(cur, m); len(diff) == 0 {
		t.Fatal("content upgrade must ask again for new permissions")
	}
}
