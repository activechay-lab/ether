package stremio

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestAddonPlayLinkRoutes(t *testing.T) {
	b64 := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	key := strings.Repeat("e", 40)
	for _, tc := range []struct {
		name, url, want string
		idx             int
	}{
		{"torrentio debrid with file index", "https://torrentio.test/resolve/realdebrid/" + key + "/" + hashA + "/null/3/Show.S01E04.mkv", hashA, 3},
		{"torrentio other service", "https://torrentio.test/resolve/easydebrid/k/" + hashA + "/null/0/m.mkv", hashA, 0},
		{"torrentio unknown service", "https://torrentio.test/resolve/mystery/k/" + hashA + "/null/0/m.mkv", "", -1},
		{"comet hash and index", "https://comet.test/" + b64(`{"debridApiKey":"k"}`) + "/playback/" + hashA + "/2/1/4/Name", hashA, 2},
		{"comet placeholder index", "https://comet.test/cfg/playback/" + hashA + "/n/n/n/Name", hashA, -1},
		{"playback hash before a file name is opaque", "https://addon.test/playback/" + key + "/file.mkv", "", -1},
		{"mediafusion encrypted secret", "https://mf.test/streaming_provider/D-" + key + "/stream/" + hashA + "/1/4", hashA, -1},
		{"mediafusion playback", "https://mf.test/streaming_provider/secret/playback/" + strings.ToUpper(hashA) + "/1/4/f.mkv", hashA, -1},
		{"mediafusion usenet guid stays opaque", "https://mf.test/streaming_provider/secret/usenet/" + key + "/1/4", "", -1},
		{"stremthru strem route", "https://st.test/stremio/torz/cfg/_/strem/" + hashA + "/5/f.mkv", hashA, 5},
		{"torbox base64 array", "https://torbox.test/u/k/" + b64(`["`+hashA+`","Movie.mkv",0]`), hashA, -1},
		{"comet base64 object", "https://comet.test/cfg/playback/" + b64(`{"infoHash":"`+hashB+`","fileIdx":7}`), hashB, 7},
		{"base64 config without hash", "https://comet.test/" + b64(`{"debridApiKey":"`+key+`"}`) + "/stream", "", -1},
		{"encrypted token", "https://addon.test/play/" + strings.Repeat("Zx9q", 20), "", -1},
		{"query hash wins, path index ignored on mismatch", "https://st.test/strem/" + hashB + "/5/f?info_hash=" + hashA, hashA, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, idx, ambiguous := urlTorrent(tc.url)
			if got != tc.want || idx != tc.idx || ambiguous {
				t.Fatalf("hash=%q idx=%d ambiguous=%v want %q/%d", got, idx, ambiguous, tc.want, tc.idx)
			}
		})
	}
	if _, _, ambiguous := urlTorrent("https://comet.test/cfg/playback/" + hashA + "/0/" + b64(`["`+hashB+`"]`)); !ambiguous {
		t.Fatal("two route hashes in one link must conflict")
	}
}

func TestAddonStreamFallbacksAndNZBLinks(t *testing.T) {
	binge := func(g, name string) *stream { return &stream{Name: name, BehaviorHints: behaviorHints{BingeGroup: g}} }
	if h, _ := streamHash(binge("comet|"+hashA, "[TB+] Comet")); h != hashA {
		t.Fatalf("comet bingeGroup: %q", h)
	}
	if h, _ := streamHash(binge("usenet-smartplay-"+hashA, "Usenet")); h != "" {
		t.Fatalf("usenet bingeGroup read as torrent: %q", h)
	}
	if h, ambiguous := streamHash(&stream{InfoHash: hashA, BehaviorHints: behaviorHints{BingeGroup: "torbox|" + hashB}}); h != hashA || ambiguous {
		t.Fatalf("bingeGroup must not override a labelled hash: %q %v", h, ambiguous)
	}
	for u, want := range map[string]bool{
		"https://idx.test/getnzb/abc.nzb?r=k":        true,
		"https://idx.test/api?t=get&id=abc&apikey=k": true,
		"https://idx.test/download/abc/Movie.NZB":    true,
		"https://addon.test/play/abc/Movie.mkv":      false,
		"https://idx.test/api?t=search&q=movie":      false,
		"magnet:?xt=urn:btih:" + hashA + "&dn=x.nzb": false,
	} {
		if got := nzbFromURL(u) != ""; got != want {
			t.Errorf("nzbFromURL(%q)=%v want %v", u, got, want)
		}
	}
	req := core.SearchRequest{Type: core.TypeEpisode, Title: "Show", Season: 1, Episode: 4}
	rels, _, _ := convertWith([]stream{
		{Name: "[RD+] Torrentio", Title: "Show.S01E04.1080p.WEB.x264-GRP", URL: "https://t.test/resolve/realdebrid/k/" + hashA + "/null/3/Show.S01E04.mkv"},
		{Name: "Usenet", Title: "Show.S01E04.720p.WEB.x264-GRP", URL: "https://u.test/play/opaque", NZBURL: "https://u.test/nzb/1.nzb"},
		{Name: "Indexer", Title: "Show.S01E04.2160p.WEB.x265-GRP", URL: "https://idx.test/api?t=get&id=9&apikey=k"},
	}, req, false, convertOptions{instance: "ix", origin: "https://u.test"})
	if len(rels) != 3 {
		t.Fatalf("releases %+v", rels)
	}
	if rels[0].GUID != "btih:"+hashA+"/3" || rels[0].StreamURL == "" {
		t.Fatalf("torrentio release %+v", rels[0])
	}
	if rels[1].Protocol != core.ProtocolUsenet || rels[1].StreamURL != "https://u.test/play/opaque" || rels[1].NZBURL == "" {
		t.Fatalf("usenet release keeps its play link: %+v", rels[1])
	}
	if rels[2].Protocol != core.ProtocolUsenet || rels[2].StreamURL != "" || rels[2].NZBURL == "" {
		t.Fatalf("NZB download url: %+v", rels[2])
	}
}
