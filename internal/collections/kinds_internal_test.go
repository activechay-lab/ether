package collections

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
)

func TestBuiltinSourceKindsAllRegistered(t *testing.T) {
	want := []string{"anilistList", "catalog", "friendsWatchlist", "letterboxdList", "mdblist", "stevenlu", "titles",
		"tmdbCollection", "tmdbCompany", "tmdbDiscover", "tmdbKeyword", "tmdbList", "tmdbNetwork",
		"tmdbPerson", "tmdbUserList", "traktList", "watchProvider"}
	reg := NewKindRegistry()
	got := reg.IDs()
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", got, want)
		}
	}
}

func TestSourceKindDescriptors(t *testing.T) {
	ds := BuiltinDescriptors()
	if len(ds) != 17 {
		t.Fatalf("descriptors = %d, want 17", len(ds))
	}
	seen := map[string]bool{}
	for _, d := range ds {
		if err := d.Validate(); err != nil {
			t.Errorf("%s: %v", d.ID, err)
		}
		if d.Kind != extend.KindCollectionSource || d.Owner != extend.Builtin {
			t.Errorf("%s: kind/owner = %s/%s", d.ID, d.Kind, d.Owner)
		}
		if d.Label.Source == "" || d.Hint.Source == "" {
			t.Errorf("%s: label and hint must use extend.T", d.ID)
		}
		meta, err := extend.MetaOf[extend.CollectionSourceMeta](d)
		if err != nil {
			t.Fatalf("%s meta: %v", d.ID, err)
		}
		var defaults map[string]any
		if err := json.Unmarshal(meta.Defaults, &defaults); err != nil {
			t.Errorf("%s defaults: %v", d.ID, err)
		}
		seen[d.ID] = true
	}
	for _, id := range []string{"catalog", "friendsWatchlist", "tmdbList", "stevenlu"} {
		if !seen[id] {
			t.Errorf("missing descriptor %s", id)
		}
	}
	// The chart and StevenLu options move wholesale from the console tables.
	for _, d := range ds {
		meta, _ := extend.MetaOf[extend.CollectionSourceMeta](d)
		switch d.ID {
		case "tmdbList":
			if len(meta.Params) != 2 || len(meta.Params[1].Options) != 8 {
				t.Errorf("tmdbList params = %+v", meta.Params)
			}
		case "stevenlu":
			if len(meta.Params) != 1 || len(meta.Params[0].Options) != 9 {
				t.Errorf("stevenlu params = %+v", meta.Params)
			}
		case "tmdbPerson":
			if len(meta.Params) != 3 || meta.Params[0].Lookup != "person" {
				t.Errorf("tmdbPerson params = %+v", meta.Params)
			}
		}
	}
}

func TestValidateUsesRegistry(t *testing.T) {
	base := core.CollectionSpec{Name: "X", MediaType: "movie", Match: "all", Sort: "popularity", Refresh: "daily", Artwork: core.CollectionArtwork{Mode: "auto"}}
	sp := base
	sp.Sources = []core.CollectionSource{{Kind: "bogus"}}
	if err := Validate(&sp); err == nil {
		t.Fatal("unknown kind accepted")
	}
	sp = base
	sp.Sources = []core.CollectionSource{{Kind: "anilistList", URL: "not a url"}}
	if err := Validate(&sp); err == nil {
		t.Fatal("bad AniList URL accepted")
	}
	sp = base
	sp.Sources = []core.CollectionSource{{Kind: "stevenlu", List: "movies"}}
	if err := Validate(&sp); err != nil {
		t.Fatalf("valid stevenlu: %v", err)
	}
}

// fakeKind proves a runtime kind can validate and gather through the
// service registry without touching any switch.
type fakeKind struct{}

func (fakeKind) ID() string { return "plugin:test/list" }
func (fakeKind) Describe() KindDescribe {
	return KindDescribe{Label: extend.T("Fake"), Defaults: `{}`, Order: 999}
}
func (fakeKind) Validate(ve *ValidationError, path string, src core.CollectionSource) {
	if src.URL == "" {
		ve.add(path+".url", "a URL is required")
	}
}
func (fakeKind) Gather(g *Gather) { g.Res.all = true }

func TestRuntimeSourceKindRegistration(t *testing.T) {
	s := &Service{kinds: NewKindRegistry()}
	if err := s.RegisterSourceKind(fakeKind{}); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterSourceKind(fakeKind{}); err == nil {
		t.Fatal("duplicate kind accepted")
	}
	sp := core.CollectionSpec{Name: "X", MediaType: "movie", Match: "all", Sort: "popularity", Refresh: "daily", Artwork: core.CollectionArtwork{Mode: "auto"},
		Sources: []core.CollectionSource{{Kind: "plugin:test/list"}}}
	if err := s.validate(&sp); err == nil {
		t.Fatal("fake kind validation not consulted")
	}
	sp.Sources[0].URL = "https://example.test/list"
	if err := s.validate(&sp); err != nil {
		t.Fatalf("valid fake kind: %v", err)
	}
	res := s.gather(context.Background(), &sp, sp.Sources[0], nil)
	if !res.all {
		t.Fatal("fake kind did not gather")
	}
	// The package-level Validate (built-ins only) must not see it.
	if err := Validate(&sp); err == nil {
		t.Fatal("default registry saw the runtime kind")
	}
}
