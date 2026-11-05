package schema_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"testing"

	"github.com/braedonsaunders/ether/internal/config/schema"
	"github.com/braedonsaunders/ether/internal/extend"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/builtins.json")

// The mock console backend (web/src/mock/extend/descriptors/settings.ts) is
// held to this same golden by a vitest, so Go and mock cannot drift apart
// without a test failing.
func TestBuiltinsMatchGolden(t *testing.T) {
	got, err := json.MarshalIndent(schema.Builtins(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if *updateGolden {
		if err := os.WriteFile("testdata/builtins.json", got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile("testdata/builtins.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, got) {
		t.Fatal("builtins differ from testdata/builtins.json; regenerate with -update and update the mock descriptors to match")
	}
}

func TestBuiltinsAreValidSchemas(t *testing.T) {
	ids := map[string]bool{}
	for _, b := range schema.Builtins() {
		if b.ID == "" || ids[b.ID] {
			t.Fatalf("duplicate or empty id %q", b.ID)
		}
		ids[b.ID] = true
		if b.Label.Empty() {
			t.Fatalf("%s: label is empty", b.ID)
		}
		if err := schema.ValidateSchema(b.Schema, false); err != nil {
			t.Fatalf("%s: %v", b.ID, err)
		}
		if len(b.Schema.Groups) == 0 {
			t.Fatalf("%s: no groups", b.ID)
		}
	}
	for _, want := range []string{
		"server", "serverCluster", "plex", "cache", "catalog", "playVersions",
		"releaseSearch", "releaseBlocks", "transcode", "trakt", "openSubtitles",
		"updates", "spike", "markers", "debrid", "downloads", "personalize",
		"extras",
	} {
		if !ids[want] {
			t.Fatalf("missing builtin schema %q", want)
		}
	}
}

func TestValidateSchemaPluginRules(t *testing.T) {
	raw := func(s string) extend.Text { return extend.Text{Source: s} }
	long := "This label is far too long for a plugin form label field"
	cases := []schema.Field{
		{Key: "blob", Label: raw("Blob"), Kind: schema.KindJSON},
		{Key: "pick", Label: raw("Pick"), Kind: schema.KindSelect},
		{Key: "dash", Label: raw("Fast—really fast"), Kind: schema.KindText},
		{Key: "long", Label: raw(long), Kind: schema.KindText},
		{Key: "bad", Label: raw("Mode"), Kind: schema.Kind("nonsense")},
		{Key: "twice", Label: raw("One"), Kind: schema.KindText, Help: raw("Two. Sentences.")},
	}
	for _, f := range cases {
		s := schema.Schema{Groups: []schema.Group{{Fields: []schema.Field{f}}}}
		if err := schema.ValidateSchema(s, true); err == nil {
			t.Fatalf("field %s (%s) passed plugin validation", f.Key, f.Kind)
		}
	}
	ok := schema.Schema{Groups: []schema.Group{{Fields: []schema.Field{
		{Key: "token", Label: extend.T("Access token"), Kind: schema.KindSecret, Help: extend.T("From your account page.")},
	}}}}
	if err := schema.ValidateSchema(ok, true); err != nil {
		t.Fatalf("clean plugin schema rejected: %v", err)
	}
}

func TestBuiltinDescriptorsRegister(t *testing.T) {
	ds, err := schema.BuiltinDescriptors()
	if err != nil {
		t.Fatal(err)
	}
	r := extend.New()
	if err := r.Register(ds...); err != nil {
		t.Fatal(err)
	}
	got := r.List(extend.KindSettingsSchema)
	if len(got) != len(schema.Builtins()) {
		t.Fatalf("got %d registered settingsSchema descriptors, want %d", len(got), len(schema.Builtins()))
	}
	for i, d := range got {
		want := schema.Builtins()[i]
		if d.ID != want.ID || d.Owner != extend.Builtin {
			t.Fatalf("descriptor %d = %s (%s), want %s (builtin)", i, d.ID, d.Owner, want.ID)
		}
		meta, err := extend.MetaOf[extend.SettingsSchemaMeta](d)
		if err != nil {
			t.Fatalf("%s: MetaOf: %v", d.ID, err)
		}
		if meta.ConfigKey != want.ConfigKey {
			t.Fatalf("%s: configKey = %q, want %q", d.ID, meta.ConfigKey, want.ConfigKey)
		}
	}
}
