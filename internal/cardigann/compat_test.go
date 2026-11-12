package cardigann

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// Real definitions use setting names that are not Go identifiers.
func TestRenderNonIdentifierFields(t *testing.T) {
	v := baseVars(map[string]any{"cat-id": "5", "2facode": "123", "sitelink": "http://x/"}, testNow)
	v["Result"] = map[string]any{"title-alt": "Alt"}
	for tpl, want := range map[string]string{
		"{{ .Config.cat-id }}":                                   "5",
		"{{ .Config.2facode }}":                                  "123",
		"c={{ if .Config.cat-id }}{{ .Config.cat-id }}{{ end }}": "c=5",
		"{{ .Result.title-alt }}":                                "Alt",
		`{{ re_replace .Config.cat-id "\d" "n" }}`:               "n",
		`{{ if eq .Config.cat-id ".Config.cat-id" }}x{{ end }}y`: "y", // literal untouched
	} {
		got, err := render(tpl, v, nil)
		if err != nil || got != want {
			t.Errorf("%s = %q, %v; want %q", tpl, got, err, want)
		}
	}
}

// .NET named Unicode blocks, used by about a hundred real definitions.
func TestNETUnicodeBlocks(t *testing.T) {
	cases := []struct {
		pattern, in, want string
	}{
		{`[\p{IsCyrillic}\W]+`, "Матрица 1999", "1999"},
		{`\p{IsCyrillic}+`, "abc Матрица", "abc "},
		{`[\p{IsCJKUnifiedIdeographs}\W]+`, "信号.The.Signal", "TheSignal"},
		{`\P{IsBasicLatin}`, "café", "caf"},
	}
	for _, c := range cases {
		re, err := compileRegex(c.pattern)
		if err != nil {
			t.Fatalf("%s: %v", c.pattern, err)
		}
		got, err := re.Replace(c.in, "", -1, -1)
		if err != nil || got != c.want {
			t.Errorf("%s on %q = %q, %v; want %q", c.pattern, c.in, got, err, c.want)
		}
	}
	if _, err := compileRegex(`[\P{IsCyrillic}]`); err == nil {
		t.Error("a negated block inside a class must be reported")
	}
	if _, err := compileRegex(`\p{IsNoSuchBlock}`); err == nil {
		t.Error("unknown block accepted")
	}
}

// YAML 1.2's "\/" escape in double-quoted strings, which go-yaml rejects.
func TestParseAcceptsSlashEscape(t *testing.T) {
	def := `---
id: slashy
name: Slashy
type: public
language: en-US
links:
  - https://slashy.test/
caps:
  categorymappings:
    - {id: 1, cat: Movies, desc: Movies}
  modes:
    search: [q]
search:
  paths:
    - path: "search\/{{ .Keywords }}"
  rows:
    selector: 'tr.row\/x'
  fields:
    title:
      selector: a
      filters:
        - name: re_replace
          args: ["N\/A", ""]
    download:
      selector: a
      attribute: href
`
	d, _, err := Parse([]byte(def))
	if err != nil {
		t.Fatal(err)
	}
	if d.Search.Paths[0].Path != "search/{{ .Keywords }}" {
		t.Errorf("path %q", d.Search.Paths[0].Path)
	}
	if d.Search.Rows.Selector != `tr.row\/x` {
		t.Errorf("single-quoted text changed: %q", d.Search.Rows.Selector)
	}
	if got := d.Search.Fields[0].Selector.Filters[0].Args[0]; got != "N/A" {
		t.Errorf("filter arg %q", got)
	}
}

// The Prowlarr Nyaa.si definition (testdata/nyaasi.yml) parses and runs
// through the engine: a real captured search page (testdata/nyaasi_search.html,
// trimmed to three rows) yields torrents with magnet, info hash, size,
// seeders and the TV/Anime category. Anime requests are also searched by
// their absolute episode number, which Nyaa releases use. Both fixtures are
// copied verbatim from the live site and the Prowlarr/Indexers repository.
func TestNyaasiDefinition(t *testing.T) {
	def := loadDef(t, "nyaasi.yml")
	if def.ID != "nyaasi" {
		t.Fatalf("id = %q", def.ID)
	}
	if !def.SupportsTV() || !def.SupportsMovies() {
		t.Fatalf("supports TV=%v Movies=%v", def.SupportsTV(), def.SupportsMovies())
	}
	page := readFixture(t, "nyaasi_search.html")
	var seen []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		mu.Lock()
		seen = append(seen, q)
		mu.Unlock()
		// The SxxExx query finds nothing (as on the real site, where the
		// anime uses absolute numbering); only the absolute form matches.
		if strings.Contains(q, "S02E03") {
			w.Write([]byte(`<html><body><table></table></body></html>`))
			return
		}
		w.Write(page)
	}))
	defer srv.Close()
	r := testRunner(t, def, srv.URL, runnerSetup{settings: map[string]string{
		"prefer_magnet_links": "true", "cat-id": "0_0", "sort": "id", "type": "desc", "filter-id": "0",
	}})
	var b budget
	rels, err := r.Search(context.Background(), core.SearchRequest{
		Type: core.TypeEpisode, Title: "Sousou no Frieren", Anime: true, Season: 2, Episode: 3, AbsoluteEpisode: 3,
	}, []int{5070}, []int{5070}, b.reserve)
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 3 {
		t.Fatalf("results = %d, want 3", len(rels))
	}
	first := rels[0]
	if first.Protocol != core.ProtocolTorrent {
		t.Fatalf("protocol = %q", first.Protocol)
	}
	if first.InfoHash != "ef3e7ad1b12bdd9fc341691d8866cd1fa8374a4b" {
		t.Fatalf("infohash = %q", first.InfoHash)
	}
	// With "prefer magnet links" on (Nyaa's default) the torrent file is
	// skipped and only the magnet is kept.
	if !strings.HasPrefix(first.MagnetURL, "magnet:") || first.NZBURL != "" {
		t.Fatalf("magnet=%q nzb=%q", first.MagnetURL, first.NZBURL)
	}
	if first.Size != 7086696038 || first.Seeders != 36 || first.Peers != 41 {
		t.Fatalf("numbers = size %d seeders %d peers %d", first.Size, first.Seeders, first.Peers)
	}
	if first.Category != 5070 {
		t.Fatalf("category = %d, want TV/Anime 5070", first.Category)
	}
	if !strings.Contains(first.Name, "Sousou no Frieren") {
		t.Fatalf("name = %q", first.Name)
	}
	if first.PubDate.IsZero() || first.PubDate.UTC().Format("2006-01-02 15:04") != "2026-09-12 15:45" {
		t.Fatalf("date = %v", first.PubDate)
	}
	// The absolute-episode query ("Sousou no Frieren 03") must have been
	// sent as a fallback form Nyaa needs, alongside the SxxExx attempt.
	mu.Lock()
	sawAbsolute := false
	for _, q := range seen {
		if strings.Contains(q, "Frieren 03") {
			sawAbsolute = true
		}
	}
	mu.Unlock()
	if !sawAbsolute {
		t.Fatalf("absolute-episode query not issued; queries = %v", seen)
	}

	// With "prefer magnet links" off, the .torrent download link is kept.
	r2 := testRunner(t, def, srv.URL, runnerSetup{id: "nyaasi-file", settings: map[string]string{
		"prefer_magnet_links": "false", "cat-id": "0_0", "sort": "id", "type": "desc", "filter-id": "0",
	}})
	var b2 budget
	rels2, err := r2.Search(context.Background(), core.SearchRequest{Type: core.TypeMovie, Title: "Sousou no Frieren"}, []int{2000}, nil, b2.reserve)
	if err != nil {
		t.Fatal(err)
	}
	if len(rels2) == 0 || !strings.HasSuffix(rels2[0].NZBURL, "/download/2160092.torrent") {
		t.Fatalf("torrent link = %q", rels2[0].NZBURL)
	}
}

// A templated search method: POST with keywords, GET without.
func TestTemplatedSearchMethod(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods = append(methods, r.Method)
		mu.Unlock()
		w.Write([]byte(`<html><body><table></table></body></html>`))
	}))
	defer srv.Close()
	def, _, err := Parse([]byte(`---
id: methody
name: Methody
type: public
language: en-US
links:
  - https://methody.test/
caps:
  categorymappings:
    - {id: 1, cat: Movies, desc: Movies}
  modes:
    search: [q]
search:
  paths:
    - path: search
      method: "{{ if .Keywords }}post{{ else }}get{{ end }}"
  inputs:
    q: "{{ .Keywords }}"
  rows:
    selector: tr
  fields:
    title:
      selector: a
    download:
      selector: a
      attribute: href
`))
	if err != nil {
		t.Fatal(err)
	}
	r := testRunner(t, def, srv.URL+"/", runnerSetup{})
	if _, err := r.Search(context.Background(), core.SearchRequest{Type: core.TypeMovie, Title: "The Signal"}, nil, nil, nil); err != nil && !strings.Contains(err.Error(), "no results") {
		t.Logf("search: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(methods) == 0 || methods[0] != http.MethodPost {
		t.Fatalf("methods %v, want POST for a keyword search", methods)
	}
}
