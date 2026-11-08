package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// An install upgraded from a release before the Radarr/Sonarr-standard
// ranking starts on exactly the profiles a fresh install has; a standard
// profile the owner made is kept.
func TestPreStandardProfilesResetToDefaults(t *testing.T) {
	open := func(doc string) Config {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		st, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		return st.Get()
	}
	old := open(`{"resolver":{"customFormatScores":{"x":5}},"quality":{"default":"Mine","profiles":[{"name":"Mine","mediaType":"movie","maxHeight":1080,"minHeight":480,"preferredSources":["webdl"],"preferredCodecs":["h264"],"preferredAudio":["aac"],"ranking":["resolution"]}]}}`)
	if want := DefaultQuality().Profiles; !reflect.DeepEqual(old.Quality.Profiles, want) {
		t.Fatalf("profiles %+v, want the defaults", old.Quality.Profiles)
	}
	if old.Quality.Default == "Mine" {
		t.Fatal("default still names the removed profile")
	}

	shared, _, err := mergeShared(Defaults(t.TempDir()), []byte(`{"quality":{"profiles":[{"name":"Mine","mediaType":"tv","preferredSources":["webdl"]}]}}`))
	if err != nil || !reflect.DeepEqual(shared.Quality.Profiles, DefaultQuality().Profiles) {
		t.Fatalf("shared profiles %+v (%v), want the defaults", shared.Quality.Profiles, err)
	}

	b, _ := json.Marshal(DefaultQuality().Profiles[1])
	var mine map[string]any
	_ = json.Unmarshal(b, &mine)
	mine["name"] = "Mine"
	doc, _ := json.Marshal(map[string]any{"quality": map[string]any{"default": "Mine", "profiles": []any{mine}}})
	kept := open(string(doc))
	if len(kept.Quality.Profiles) != 1 || kept.Quality.Profiles[0].Name != "Mine" || kept.Quality.Default != "Mine" {
		t.Fatalf("standard profile not kept: %+v", kept.Quality)
	}
}
