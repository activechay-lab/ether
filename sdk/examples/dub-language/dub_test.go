package dublanguage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/studio"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/store"
)

func files(t *testing.T) (plugins.Manifest, []byte, []byte) {
	t.Helper()
	raw, e := os.ReadFile("manifest.json")
	if e != nil {
		t.Fatal(e)
	}
	m, e := plugins.ParseManifest(raw)
	if e != nil {
		t.Fatal(e)
	}
	js, e := os.ReadFile("dist/plugin.js")
	if e != nil {
		t.Fatal(e)
	}
	return m, raw, js
}

// TestHostedRanking500 ranks a full 500-release batch through the hosted
// runtime under the production budgets (2 ms per candidate, 50 ms per
// batch) and requires every candidate to finish inside them. The engine
// judges the budgets by the plugin's own time (the thread's CPU time when
// the wall clock says a budget ran out), so a busy or race-instrumented
// host does not count against the plugin; a plugin that is itself too slow
// still fails here. Facts carry real release names and the languages
// Ether's parser reads from them, so the batch is as heavy as production.
func TestHostedRanking500(t *testing.T) {
	m, _, js := files(t)
	eng := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer eng.Close()
	rt, err := plugins.HostedRuntimeFactory(eng)(plugins.Installed{ID: m.ID, Manifest: m, Files: func(p string) ([]byte, bool) { return js, p == "dist/plugin.js" }})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	hot := rt.(plugins.HotRuntime)
	releases := []struct {
		name string
		key  int64 // with French then German wanted
	}{
		{"The.French.Dispatch.2021.1080p.BluRay.DTS-HD.MA.5.1.x264-GRP", 0},
		{"Some.Movie.2019.FRENCH.1080p.WEB-DL.DDP5.1.H.264-GRP", 2},
		{"Some.Movie.2019.MULTi.2160p.WEB-DL.DDP5.1.Atmos.DV.HDR.H.265-GROUP", 2},
		{"Some.Movie.2019.GERMAN.DL.1080p.WEB.H264-DEFLATE", 1},
		{"Some.Movie.2019.1080p.AMZN.WEB-DL.DDP5.1.H.264-NTb", 0},
	}
	c := make([]plugins.CandidateFacts, 500)
	for i := range c {
		r := releases[i%len(releases)]
		info := releaseparse.Parse(r.name)
		c[i] = plugins.CandidateFacts{ReleaseID: fmt.Sprint(i), Name: r.name, Size: 12_000_000_000, Protocol: "usenet",
			SourceClass: "web", Indexer: "indexer", Resolution: fmt.Sprintf("%dp", info.Height), VideoCodec: info.VideoCodec,
			AudioCodecs: []string{info.AudioCodec}, AudioChannels: info.Channels, Languages: info.Languages, Group: info.Group}
	}
	keys, out, err := hot.RankBatch(context.Background(), c, json.RawMessage(`{"value":["fr","de"]}`))
	if err != nil || out.Status != "ok" {
		t.Fatalf("rank: %+v %v", out, err)
	}
	if out.Slow != 0 {
		t.Fatalf("%d of 500 candidates went over budget: %+v", out.Slow, out)
	}
	for i, k := range keys {
		if want := releases[i%len(releases)].key; k != want {
			t.Fatalf("key %d (%s) = %d, want %d", i, c[i].Name, k, want)
		}
	}
}
func TestStudioReplayChangesPick(t *testing.T) {
	_, raw, js := files(t)
	db := store.OpenTest(t)
	eng := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer eng.Close()
	runner := studio.NewRunner(studio.ReplayDeps{DB: db, Engine: eng})
	defer runner.Close()
	tr := resolver.Trace{TitleName: "Fixture", Profile: "default", At: time.Now(), Winner: "a", Candidates: []resolver.CandidateTrace{{ReleaseID: "a", Name: "Film.1080p.WEB-DL", Score: 100, Height: 1080}, {ReleaseID: "b", Name: "Film.FRENCH.DUB.1080p.WEB-DL", Score: 90, Height: 1080}}}
	encoded, _ := json.Marshal(tr)
	_, err := db.Exec(`INSERT INTO resolver_history(id,resolved_at,rating_key,title_name,profile,release_id,release_name,indexer,size,error,failover,pinned,candidates,trace) VALUES (1,1,1,'Fixture','default','a','Film.1080p.WEB-DL','test',1,'',0,0,2,?)`, string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := runner.Replay(context.Background(), studio.ReplayRequest{DevInstance: &studio.DevInstance{Manifest: raw, Files: map[string]string{"dist/plugin.js": string(js)}}, Kind: studio.ReplayRanking, Params: studio.ReplayParams{StepKey: "dub_language", Config: json.RawMessage(`{"value":["fr"]}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Ranking == nil || rep.Ranking.ChangedCount != 1 {
		t.Fatalf("replay: %+v", rep.Ranking)
	}
}
