package collections

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// Regenerate the mock from real normalized specs with -update; never hand-copy
// template names, rules or bundle membership into the web mock.
func TestMockTemplatesMatchGo(t *testing.T) {
	want := struct {
		Templates any `json:"templates"`
		Bundles   any `json:"bundles"`
	}{Templates(), TemplateBundles()}
	raw, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	const path = "../../web/src/mock/templates.json"
	if *updateMock {
		if err := os.WriteFile(path, append(raw, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("template mock drift: run TestMockTemplatesMatchGo -update")
	}
}
