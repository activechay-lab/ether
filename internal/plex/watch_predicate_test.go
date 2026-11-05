package plex

import (
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
)

func TestUnwatchedSeasonChildrenUseLeaves(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	f.state.set(ownerUser.ID, core.WatchStatus{Key: bbS1.Key, ViewCount: 7})
	f.state.set(ownerUser.ID, core.WatchStatus{Key: bbE1.Key, ViewCount: 1})
	path := "/library/metadata/" + breakingBad.Key.String() + "/children"
	for _, tc := range []struct {
		query string
		want  int
	}{{"unwatched=1", 1}, {"unwatched=0", 0}, {"unwatched=2", 0}, {"unwatched!=0", 1}} {
		rec := f.get(t, path+"?"+tc.query, jsonAccept...)
		var out struct{ MediaContainer struct{ Metadata []any } }
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.MediaContainer.Metadata) != tc.want {
			t.Fatalf("%s got %s", tc.query, rec.Body.String())
		}
	}
}
func TestWatchFilterBooleanGrammar(t *testing.T) {
	for _, tc := range []struct {
		op, v string
		want  bool
	}{{"=", "1", true}, {"!=", "0", true}, {"=", "true", false}, {"=", "01", false}, {"==", "1", false}, {">>", "0", false}} {
		got, _ := parseBoolFilter(tc.op, tc.v)
		if got != tc.want {
			t.Fatalf("%s %s =%t want%t", tc.op, tc.v, got, tc.want)
		}
	}
}
