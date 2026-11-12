package newznab

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

const zileanCapsXML = `<?xml version="1.0" encoding="UTF-8"?>
<caps>
  <server title="Zilean" type="torznab"/>
  <limits max="200" default="100"/>
  <searching>
    <search available="yes" supportedParams="q"/>
    <movie-search available="yes" supportedParams="q,imdbid,year"/>
    <tv-search available="yes" supportedParams="q,imdbid,season,ep,year"/>
  </searching>
  <categories>
    <category id="2000" name="Movies"><subcat id="2040"/></category>
    <category id="5000" name="TV"><subcat id="5040"/></category>
  </categories>
</caps>`

const zileanFeedXML = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed">
  <channel>
    <item>
      <title>%s</title>
      <guid>%s</guid>
      <pubDate>Mon, 02 Jan 2023 15:04:05 +0000</pubDate>
      <category>%d</category>
      <torznab:attr name="infohash" value="%s"/>
      <torznab:attr name="magneturl" value="magnet:?xt=urn:btih:%s"/>
      <torznab:attr name="size" value="8000000000"/>
    </item>
  </channel>
</rss>`

// fakeZilean serves Zilean's anonymous Torznab API at /torznab/api.
type fakeZilean struct {
	mu    sync.Mutex
	paths []string
	param []string // the "t" and "imdbid"/"season"/"ep" values of the last search
	srv   *httptest.Server
}

func newFakeZilean(t *testing.T) *fakeZilean {
	t.Helper()
	z := &fakeZilean{}
	mux := http.NewServeMux()
	mux.HandleFunc("/torznab/api", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		z.mu.Lock()
		z.paths = append(z.paths, r.URL.Path)
		z.param = append(z.param, q.Get("t"), q.Get("imdbid"), q.Get("season"), q.Get("ep"), q.Get("q"))
		z.mu.Unlock()
		switch q.Get("t") {
		case "caps":
			fmt.Fprint(w, zileanCapsXML)
		default:
			// Zilean answers every search with a standard Torznab feed whose
			// items carry an infohash and magnet — exactly the debrid path's
			// candidates.
			fmt.Fprintf(w, zileanFeedXML, "The.Matrix.1999.1080p.BluRay.x264-GRP", "z-1", 2040,
				"ABCDEF0123456789ABCDEF0123456789ABCDEF01", "abcdef0123456789abcdef0123456789abcdef01")
		}
	})
	z.srv = httptest.NewServer(mux)
	t.Cleanup(z.srv.Close)
	return z
}

func zileanConfig(url string) config.Indexer {
	return config.Indexer{ID: "z1", Name: "Zilean", URL: url, Kind: config.IndexerKindZilean, Protocol: "torrent", Enabled: true}
}

// TestZileanSearchesTorznabAPI proves a Zilean indexer is queried through
// its Torznab endpoint built from the owner-entered base URL, and returns
// the indexed infohashes the debrid pipeline needs.
func TestZileanSearchesTorznabAPI(t *testing.T) {
	z := newFakeZilean(t)
	c := New(zileanConfig(z.srv.URL), nil, Options{})
	// The owner pastes the instance base URL; /torznab/api is appended.
	res, err := c.Search(context.Background(), core.SearchRequest{
		Type: core.TypeMovie, IDs: core.IDs{IMDB: "tt0133093"}, Title: "The Matrix", Year: 1999,
	})
	if err != nil {
		t.Fatal(err)
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	for _, p := range z.paths {
		if p != "/torznab/api" {
			t.Fatalf("Zilean queried at %q, want /torznab/api", p)
		}
	}
	if len(res) == 0 {
		t.Fatal("no results from Zilean Torznab feed")
	}
	if res[0].InfoHash != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Fatalf("info hash = %q", res[0].InfoHash)
	}
	if res[0].Protocol != core.ProtocolTorrent {
		t.Fatalf("protocol = %q, want torrent", res[0].Protocol)
	}
}

// TestZileanTorznabBase pins how the owner's base URL is turned into the
// Torznab endpoint, including the double-append guard for edited entries.
func TestZileanTorznabBase(t *testing.T) {
	cases := map[string]string{
		"http://zilean:8181":             "http://zilean:8181/torznab",
		"http://zilean:8181/":            "http://zilean:8181/torznab",
		"http://zilean:8181/torznab":     "http://zilean:8181/torznab",
		"http://zilean:8181/torznab/api": "http://zilean:8181/torznab",
		"http://zilean:8181/api":         "http://zilean:8181/torznab",
		" http://zilean:8181/some/base/": "http://zilean:8181/some/base/torznab",
	}
	for in, want := range cases {
		if got := zileanTorznabBase(in); got != want {
			t.Errorf("zileanTorznabBase(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestZileanIsTorrentSource pins that a Zilean indexer counts as a torrent
// source, so debrid availability and the cached-only path apply to it.
func TestZileanIsTorrentSource(t *testing.T) {
	ix := zileanConfig("http://zilean:8181")
	if !ix.IsZilean() || !ix.IsTorrent() {
		t.Fatalf("IsZilean=%v IsTorrent=%v", ix.IsZilean(), ix.IsTorrent())
	}
	if ix.UsesBackend() {
		t.Fatal("Zilean is a plain API indexer and must not use a backend")
	}
}
