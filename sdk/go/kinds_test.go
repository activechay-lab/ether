package etherplugin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	etherplugin "github.com/braedonsaunders/ether/sdk/go"
	"github.com/braedonsaunders/ether/sdk/go/kit/feedparse"
	"github.com/braedonsaunders/ether/sdk/go/kit/hints"
)

type kindsFixture struct {
	Kind  json.RawMessage        `json:"kind"`
	Items []etherplugin.KindItem `json:"items"`
	Calls []kindsFixtureCall     `json:"calls"`
}

type kindsFixtureCall struct {
	Name               string          `json:"name"`
	Method             string          `json:"method"`
	Path               string          `json:"path"`
	Body               json.RawMessage `json:"body"`
	ExpectStatus       int             `json:"expectStatus"`
	Expect             json.RawMessage `json:"expect"`
	ExpectIDs          []string        `json:"expectIds"`
	ExpectComplete     *bool           `json:"expectComplete"`
	ExpectTopRef       string          `json:"expectTopRef"`
	ExpectBodyContains string          `json:"expectBodyContains"`
}

func loadKindsFixture(t *testing.T) kindsFixture {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "conformance", "kinds", "fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx kindsFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	return fx
}

// datasetProvider is the generic fixture dataset server every SDK
// re-implements; it proves the SDK routes carry the contract.
type datasetProvider struct{ items []etherplugin.KindItem }

func (d *datasetProvider) byID(id string) (etherplugin.KindItem, bool) {
	for _, it := range d.items {
		if it.ID == id {
			return it, true
		}
	}
	return etherplugin.KindItem{}, false
}

func pageOf(items []etherplugin.KindItem, cursor string, limit int) etherplugin.KindPage {
	if limit <= 0 {
		limit = 200
	}
	off, _ := strconv.Atoi(cursor)
	if off > len(items) {
		off = len(items)
	}
	end := off + limit
	page := etherplugin.KindPage{}
	if end >= len(items) {
		page.Items = append(page.Items, items[off:]...)
	} else {
		page.Items = append(page.Items, items[off:end]...)
		page.NextCursor = strconv.Itoa(end)
	}
	sort.SliceStable(page.Items, func(i, j int) bool { return page.Items[i].Index < page.Items[j].Index })
	return page
}

func (d *datasetProvider) Browse(_ *etherplugin.Context, req etherplugin.KindBrowseRequest) (etherplugin.KindPage, error) {
	var matched []etherplugin.KindItem
	for _, it := range d.items {
		if it.Level == req.Level && it.ParentID == req.ParentID {
			matched = append(matched, it)
		}
	}
	return pageOf(matched, req.Cursor, req.Limit), nil
}

func (d *datasetProvider) Search(_ *etherplugin.Context, req etherplugin.KindBrowseRequest) (etherplugin.KindPage, error) {
	var matched []etherplugin.KindItem
	for _, it := range d.items {
		if strings.Contains(strings.ToLower(it.Title), strings.ToLower(req.Query)) {
			matched = append(matched, it)
		}
	}
	return pageOf(matched, req.Cursor, req.Limit), nil
}

func (d *datasetProvider) Detail(_ *etherplugin.Context, _, id string) (etherplugin.KindItem, error) {
	if it, ok := d.byID(id); ok {
		return it, nil
	}
	return etherplugin.KindItem{}, etherplugin.ErrKindNotFound
}

func (d *datasetProvider) Changes(_ *etherplugin.Context, _ etherplugin.KindChangesRequest) (etherplugin.KindChangePage, error) {
	return etherplugin.KindChangePage{Upserts: d.items, Complete: true}, nil
}

type itemHints struct {
	Member  string `json:"member"`
	PageURL string `json:"pageUrl"`
}
type albumHints struct {
	ZipURL  string `json:"zipUrl"`
	ZipSize int64  `json:"zipSize"`
}

func (d *datasetProvider) Resolve(_ *etherplugin.Context, req etherplugin.KindResolveRequest) (etherplugin.KindResolveResponse, error) {
	var ih itemHints
	if err := hints.Unmarshal(req.Item.Hints, &ih); err != nil {
		return etherplugin.KindResolveResponse{}, err
	}
	var sources []etherplugin.PlayableSource
	if parent, ok := d.byID(req.Item.ParentID); ok {
		var ah albumHints
		_ = hints.Unmarshal(parent.Hints, &ah)
		if ah.ZipURL != "" && ih.Member != "" {
			sources = append(sources, etherplugin.PlayableSource{
				ID: "zip", Pattern: "container", MIME: "audio/flac",
				Container: &etherplugin.ContainerRef{
					Format:  "zip",
					Volumes: []etherplugin.VolumeRef{{URL: ah.ZipURL, Size: ah.ZipSize}},
					Member:  ih.Member,
				},
			})
		}
	}
	if ih.PageURL != "" {
		sources = append(sources, etherplugin.PlayableSource{ID: "direct", Pattern: "range", MIME: "audio/flac", URL: ih.PageURL})
	}
	return etherplugin.KindResolveResponse{Sources: sources}, nil
}

func (d *datasetProvider) Match(_ *etherplugin.Context, req etherplugin.KindMatchRequest) (etherplugin.KindMatchResponse, error) {
	var scores []etherplugin.KindMatchScore
	for _, c := range req.Candidates {
		score, reasons := 0.1, []string{"Title does not match"}
		if strings.Contains(strings.ToLower(c.Title), strings.ToLower(req.Item.Title)) {
			score, reasons = 0.95, []string{"Title matches"}
		}
		scores = append(scores, etherplugin.KindMatchScore{CandidateRef: c.CandidateRef(), Score: score, Reasons: reasons})
	}
	sort.SliceStable(scores, func(i, j int) bool { return scores[i].Score > scores[j].Score })
	return etherplugin.KindMatchResponse{Matches: scores}, nil
}

type datasetSetup struct{}

func (datasetSetup) TestSetup(_ *etherplugin.Context, req etherplugin.SetupTestRequest) (etherplugin.SetupTestResult, error) {
	if fmt.Sprint(req.Fields["apiKey"]) != "" {
		return etherplugin.SetupTestResult{OK: true, Detail: "Connected"}, nil
	}
	return etherplugin.SetupTestResult{OK: false, Detail: "Add your API key first", Fields: map[string]string{"apiKey": "Required"}}, nil
}

type datasetFrontend struct{}

func (datasetFrontend) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><feed><title>Field Recordings</title></feed>`))
	})
}

func jsonEqual(t *testing.T, want json.RawMessage, got []byte) {
	t.Helper()
	var w, g any
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("fixture expect: %v", err)
	}
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("response body %s: %v", got, err)
	}
	if !reflect.DeepEqual(w, g) {
		wb, _ := json.Marshal(w)
		t.Fatalf("body mismatch\nwant %s\ngot  %s", wb, got)
	}
}

func TestKindsConformanceFixture(t *testing.T) {
	fx := loadKindsFixture(t)
	var decl etherplugin.MediaKindMeta
	if err := json.Unmarshal(fx.Kind, &decl); err != nil {
		t.Fatal(err)
	}
	plugin := etherplugin.New(etherplugin.Definition{
		Manifest: etherplugin.Manifest{
			ID:      "community.example.recordings",
			Version: "1.0.0",
			Name:    "Field Recordings",
			Ether: &etherplugin.EtherManifestBlock{
				Protocol: 1, Kind: etherplugin.KindContent, Summary: "Fixture provider",
				Runtime:      etherplugin.RuntimeDecl{Kind: "remote"},
				Capabilities: []etherplugin.Capability{etherplugin.CapCatalogProvider, etherplugin.CapKindResolver, etherplugin.CapMatcher, etherplugin.CapFrontendAdapter},
				Contributes:  &etherplugin.Contributes{MediaKinds: []etherplugin.MediaKindMeta{decl}},
			},
		},
		Host: etherplugin.NewMockHost(),
	})
	prov := &datasetProvider{items: fx.Items}
	plugin.OnCatalog("recordings", prov)
	plugin.OnResolve("recordings", prov)
	plugin.OnMatch("recordings", prov)
	plugin.OnSetupTest(datasetSetup{})
	plugin.OnFrontend("feed", datasetFrontend{})

	for _, call := range fx.Calls {
		t.Run(call.Name, func(t *testing.T) {
			resp := plugin.Dispatch(context.Background(), etherplugin.RawRequest{Method: call.Method, Path: call.Path, Body: call.Body})
			if resp.Status != call.ExpectStatus {
				t.Fatalf("status %d, want %d; body %s", resp.Status, call.ExpectStatus, resp.Body)
			}
			switch {
			case call.Expect != nil:
				jsonEqual(t, call.Expect, resp.Body)
			case call.ExpectIDs != nil:
				var page etherplugin.KindChangePage
				if err := json.Unmarshal(resp.Body, &page); err != nil {
					t.Fatal(err)
				}
				var ids []string
				for _, it := range page.Upserts {
					ids = append(ids, it.ID)
				}
				if !reflect.DeepEqual(ids, call.ExpectIDs) {
					t.Fatalf("ids %v, want %v", ids, call.ExpectIDs)
				}
				if call.ExpectComplete != nil && page.Complete != *call.ExpectComplete {
					t.Fatalf("complete %v, want %v", page.Complete, *call.ExpectComplete)
				}
			case call.ExpectTopRef != "":
				var res etherplugin.KindMatchResponse
				if err := json.Unmarshal(resp.Body, &res); err != nil {
					t.Fatal(err)
				}
				if len(res.Matches) == 0 || res.Matches[0].CandidateRef != call.ExpectTopRef {
					t.Fatalf("top match %+v, want ref %s", res.Matches, call.ExpectTopRef)
				}
			case call.ExpectBodyContains != "":
				if !strings.Contains(string(resp.BinaryBody)+string(resp.Body), call.ExpectBodyContains) {
					t.Fatalf("body %s%s lacks %q", resp.BinaryBody, resp.Body, call.ExpectBodyContains)
				}
			}
		})
	}
}

func TestKindHostMockAndKit(t *testing.T) {
	host := etherplugin.NewMockHost()
	ctx := context.Background()
	accepted, err := host.KindUpsert(ctx, "recordings", []etherplugin.KindItem{{ID: "a", Level: "track", Title: "A"}})
	if err != nil || accepted != 1 {
		t.Fatalf("upsert: %d %v", accepted, err)
	}
	page, err := host.KindItems(ctx, "recordings", "", 10)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("items: %+v %v", page.Items, err)
	}
	ref, err := host.CachePut(ctx, "pk-x-recordings", "a.flac", []byte("bytes"))
	if err != nil || !strings.HasPrefix(ref, "blob:") {
		t.Fatalf("cache put: %q %v", ref, err)
	}
	if got, err := host.CacheOpen(ctx, ref); err != nil || string(got) != "bytes" {
		t.Fatalf("cache open: %q %v", got, err)
	}
	if err := host.KindDelete(ctx, "recordings", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	feed, err := feedparse.Parse([]byte(`<rss><channel><title>T</title><item><title>E</title><guid>g1</guid><enclosure url="https://x/e.mp3"/></item></channel></rss>`))
	if err != nil || len(feed.Entries) != 1 || feed.Entries[0].Audio == "" {
		t.Fatalf("feed: %+v %v", feed, err)
	}
	items, err := feed.Items("show", "track", "https://x/feed")
	if err != nil || len(items) != 2 || !items[1].Playable {
		t.Fatalf("feed items: %+v %v", items, err)
	}
}
