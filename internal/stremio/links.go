package stremio

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/newznab"
)

// Add-on play links that name their release.
//
// Debrid-keyed add-ons answer a play URL instead of an infoHash: opening it
// makes the add-on add the torrent to the viewer's account. Many of those
// URLs still say which torrent (and file) they are, in a fixed place of a
// known route. Reading it turns the stream into a native torrent candidate:
// Ether's side-effect-free cached checks, ranking and failover then apply,
// and the add-on link stays as the release's StreamURL for servers without
// a debrid account of their own (debrid.Indexer plays it then).
//
// Only labelled positions count. Arbitrary 40-hex path or query values can
// be credentials or opaque ids, and an encrypted token is never a hash.
// Recognised (concepts from the add-ons' public URL shapes):
//
//	Torrentio, KnightCrawler, Peerflix  /resolve/{service}/{key}/{hash}/{cache}/{fileIdx}/{file}
//	Comet                               /{config}/playback/{hash}/{fileIdx|n}/…  and bingeGroup "comet|{hash}"
//	MediaFusion                         /streaming_provider/{secret}/{stream|playback}/{hash}[/…]
//	StremThru (Torz)                    …/strem/{hash}[/{fileIdx}]/…
//	TorBox, Comet (older)               a base64 JSON path segment: ["{hash}", …] or {"infoHash": …, "fileIdx": …}
//	StremThru Store, TorBox             bingeGroup ending in "|{hash}" (also ":" / "-")
//	any                                 ?infoHash= / ?info_hash= / ?hash= / ?btih= / ?magnet=, magnet: URLs
//
// Usenet: a stream whose url is itself an NZB download (a path ending in
// .nzb, a /getnzb/ route, or a Newznab ?t=get&id= API call) is read as the
// release's NZB.

// resolveServices are the debrid service names Torrentio-style resolvers
// put after /resolve/.
var resolveServices = map[string]bool{
	"realdebrid": true, "alldebrid": true, "premiumize": true, "torbox": true,
	"debridlink": true, "offcloud": true, "easydebrid": true, "putio": true,
}

// hex40 returns s lower-cased when it is exactly a 40-hex v1 info hash.
// Positional path reads use it rather than newznab.NormalizeInfoHash: a
// 32-character base32 value in a path is as likely to be a key.
func hex40(s string) string {
	if len(s) != 40 {
		return ""
	}
	if _, err := hex.DecodeString(s); err != nil {
		return ""
	}
	return strings.ToLower(s)
}

// fileIndex parses a non-negative file index path segment; -1 when it is
// not one ("n", "null", a name).
func fileIndex(s string) int {
	if s == "" || len(s) > 6 {
		return -1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return -1
	}
	return n
}

// placeholderIndex reports a segment add-ons use for "no file index".
func placeholderIndex(s string) bool {
	switch strings.ToLower(s) {
	case "n", "null", "none", "undefined", "-1":
		return true
	}
	return fileIndex(s) >= 0
}

// linkMatch accumulates hashes read from one link; distinct hashes make it
// ambiguous. idx is the first file index read with the agreed hash.
type linkMatch struct {
	hash      string
	idx       int
	ambiguous bool
}

func (m *linkMatch) add(hash string, idx int) {
	if hash == "" || m.ambiguous {
		return
	}
	if m.hash != "" && m.hash != hash {
		m.hash, m.idx, m.ambiguous = "", -1, true
		return
	}
	m.hash = hash
	if m.idx < 0 && idx >= 0 {
		m.idx = idx
	}
}

// pathTorrent reads the known add-on routes in a URL path (see above).
func pathTorrent(rawPath string) linkMatch {
	m := linkMatch{idx: -1}
	parts := strings.Split(strings.Trim(rawPath, "/"), "/")
	seg := func(i int) string {
		if i < 0 || i >= len(parts) {
			return ""
		}
		s, err := url.PathUnescape(parts[i])
		if err != nil {
			return ""
		}
		return s
	}
	for i := range parts {
		switch strings.ToLower(seg(i)) {
		case "resolve":
			// Torrentio: the credential sits before the hash; only the
			// service name anchors the position.
			if resolveServices[strings.ToLower(seg(i+1))] {
				if h := newznab.NormalizeInfoHash(seg(i + 3)); h != "" {
					m.add(h, fileIndex(seg(i+5)))
				}
			}
		case "playback":
			// Comet: a hash followed by a file index or its placeholder.
			// A hash-shaped value before a file name is not enough, and
			// MediaFusion's own /playback/{hash}/{season} is read below.
			if strings.EqualFold(seg(i-2), "streaming_provider") {
				continue
			}
			if h := hex40(seg(i + 1)); h != "" && placeholderIndex(seg(i+2)) {
				m.add(h, fileIndex(seg(i+2)))
			}
		case "streaming_provider":
			// MediaFusion: the secret (often encrypted) is never read.
			if v := strings.ToLower(seg(i + 2)); v == "stream" || v == "playback" {
				m.add(hex40(seg(i+3)), -1)
			}
		case "strem":
			// StremThru: …/strem/{hash}/{fileIdx}/…
			if h := hex40(seg(i + 1)); h != "" {
				m.add(h, fileIndex(seg(i+2)))
			}
		}
	}
	for i := range parts {
		if h, idx := base64Torrent(seg(i)); h != "" {
			m.add(h, idx)
		}
	}
	return m
}

// base64Torrent reads a path segment that is base64 JSON naming a torrent:
// an array whose first element is the hash (TorBox), or an object with an
// info-hash field and optionally a file index (Comet's older payloads).
// Encrypted tokens do not decode to JSON and stay opaque.
func base64Torrent(s string) (string, int) {
	if len(s) < 56 || len(s) > 4096 {
		return "", -1
	}
	var raw []byte
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			raw = b
			break
		}
	}
	if len(raw) == 0 || (raw[0] != '[' && raw[0] != '{') || !json.Valid(raw) {
		return "", -1
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		var first string
		if len(list) > 0 && json.Unmarshal(list[0], &first) == nil {
			return hex40(first), -1
		}
		return "", -1
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return "", -1
	}
	hash := ""
	idx := -1
	for k, v := range obj {
		switch strings.ToLower(k) {
		case "infohash", "info_hash", "hash", "btih":
			var s string
			if json.Unmarshal(v, &s) == nil {
				if h := hex40(s); h != "" {
					if hash != "" && hash != h {
						return "", -1
					}
					hash = h
				}
			}
		case "fileidx", "file_idx", "fileindex", "file_index", "index":
			var n int
			if json.Unmarshal(v, &n) == nil && n >= 0 {
				idx = n
			}
		}
	}
	if hash == "" {
		return "", -1
	}
	return hash, idx
}

// bingeTorrent reads a torrent hash an add-on puts at the end of its
// behaviorHints.bingeGroup ("comet|<hash>", "torbox|<hash>", StremThru
// Store). bingeGroup only groups streams for autoplay; it never carries a
// credential. Usenet streams are skipped: their groups may end in an NZB
// digest.
func bingeTorrent(s *stream) string {
	g := strings.TrimSpace(s.BehaviorHints.BingeGroup)
	if len(g) < 41 || httpLink(s.NZBURL) != "" {
		return ""
	}
	lower := strings.ToLower(g + " " + s.Name)
	if strings.Contains(lower, "usenet") || strings.Contains(lower, "nzb") || strings.Contains(lower, "newz") {
		return ""
	}
	switch g[len(g)-41] {
	case '|', ':', '-', '/':
		return hex40(g[len(g)-40:])
	}
	return ""
}

// nzbFromURL returns raw when the stream's url is an NZB download rather
// than a playable stream: a path ending in .nzb, a /getnzb/ route, or a
// Newznab API ?t=get&id= call. Such a link never plays as video.
func nzbFromURL(raw string) string {
	link := httpLink(raw)
	if link == "" {
		return ""
	}
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	p := strings.ToLower(u.Path)
	if strings.HasSuffix(p, ".nzb") || strings.Contains(p, "/getnzb/") {
		return link
	}
	q := u.Query()
	if strings.HasSuffix(p, "/api") && strings.EqualFold(q.Get("t"), "get") && q.Get("id") != "" {
		return link
	}
	return ""
}
