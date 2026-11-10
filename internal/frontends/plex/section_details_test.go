package plex

import (
	"encoding/json"
	"testing"
)

// Plex Web's library details (Advanced Filters) read the filter schema at
// the root of the section details response; a nested Meta left its spinner
// running forever (#273). The browse endpoint keeps the nested Meta.
func TestSectionDetailsSchemaAtRoot(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	var details struct {
		MediaContainer struct {
			Type      []json.RawMessage
			FieldType []json.RawMessage
			Meta      json.RawMessage
		}
	}
	rec := f.get(t, "/library/sections/1?includeDetails=1&includeAdvanced=1", jsonAccept...)
	if err := json.Unmarshal(rec.Body.Bytes(), &details); err != nil {
		t.Fatal(err)
	}
	mc := details.MediaContainer
	if len(mc.Type) == 0 || len(mc.FieldType) == 0 || mc.Meta != nil {
		t.Fatalf("section details: Type=%d FieldType=%d Meta=%s", len(mc.Type), len(mc.FieldType), mc.Meta)
	}
	var browse struct {
		MediaContainer struct {
			Meta *struct{ Type, FieldType []json.RawMessage }
		}
	}
	rec = f.get(t, "/library/sections/1/all?type=1&includeMeta=1&X-Plex-Container-Start=0&X-Plex-Container-Size=1", jsonAccept...)
	if err := json.Unmarshal(rec.Body.Bytes(), &browse); err != nil {
		t.Fatal(err)
	}
	if m := browse.MediaContainer.Meta; m == nil || len(m.Type) == 0 {
		t.Fatalf("browse lost its nested Meta: %s", rec.Body.String())
	}
}
