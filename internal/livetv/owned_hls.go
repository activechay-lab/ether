package livetv

import (
	"bufio"
	"bytes"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// mediaPlaylist is a parsed provider media playlist.
type mediaPlaylist struct {
	target   time.Duration
	sequence int64 // EXT-X-MEDIA-SEQUENCE
	segments []playlistSegment
	ended    bool
}

type playlistSegment struct {
	msn      int64 // media sequence number in the provider's numbering
	uri      string
	dur      time.Duration
	disc     bool // EXT-X-DISCONTINUITY before it
	gap      bool
	rangeLen int64 // EXT-X-BYTERANGE length; 0 = whole resource
	rangeOff int64
	key      hlsKey
	mapURI   string
	mapLen   int64
	mapOff   int64
}

type hlsKey struct {
	method   string // "" = NONE
	uri      string
	iv       string
	format   string
	versions string
}

// hlsVariant is one EXT-X-STREAM-INF entry of a master playlist.
type hlsVariant struct {
	uri       string
	bandwidth int
	width     int
	height    int
	codecs    string
	audio     string // AUDIO group ID
}

type masterPlaylist struct {
	variants []hlsVariant
	// demuxedAudio lists AUDIO group IDs whose renditions have their own URI.
	demuxedAudio map[string]bool
}

func isMasterPlaylist(body []byte) bool {
	return bytes.Contains(body, []byte("#EXT-X-STREAM-INF"))
}

// hlsAttrs parses an attribute list: KEY=VALUE,KEY="quoted,value".
func hlsAttrs(s string) map[string]string {
	out := map[string]string{}
	for len(s) > 0 {
		eq := strings.IndexByte(s, '=')
		if eq <= 0 {
			break
		}
		key := strings.TrimSpace(s[:eq])
		s = s[eq+1:]
		var val string
		if strings.HasPrefix(s, `"`) {
			end := strings.IndexByte(s[1:], '"')
			if end < 0 {
				val, s = s[1:], ""
			} else {
				val, s = s[1:1+end], s[2+end:]
			}
			s = strings.TrimPrefix(s, ",")
		} else {
			comma := strings.IndexByte(s, ',')
			if comma < 0 {
				val, s = s, ""
			} else {
				val, s = s[:comma], s[comma+1:]
			}
		}
		out[strings.ToUpper(key)] = strings.TrimSpace(val)
	}
	return out
}

func resolveHLS(base *url.URL, raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	s := u.String()
	return s, validHTTP(s) || base == nil
}

func parseByteRange(v string) (n, off int64, hasOff bool) {
	lenPart, offPart, ok := strings.Cut(v, "@")
	n, _ = strconv.ParseInt(strings.TrimSpace(lenPart), 10, 64)
	if ok {
		off, _ = strconv.ParseInt(strings.TrimSpace(offPart), 10, 64)
	}
	return n, off, ok
}

// parseMediaPlaylist parses a media playlist and resolves URIs against base. A nil
// base keeps URIs verbatim (local relay origins).
func parseMediaPlaylist(body []byte, base *url.URL) (mediaPlaylist, bool) {
	var m mediaPlaylist
	if !bytes.HasPrefix(bytes.TrimSpace(body), []byte("#EXTM3U")) {
		return m, false
	}
	var cur playlistSegment
	var key hlsKey
	var mapURI string
	var mapLen, mapOff int64
	var nextOff int64
	pendingDisc, pendingGap := false, false
	var dur time.Duration
	haveInf := false
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
		case strings.HasPrefix(line, "#EXT-X-TARGETDURATION:"):
			if v, err := strconv.ParseFloat(strings.TrimPrefix(line, "#EXT-X-TARGETDURATION:"), 64); err == nil && v > 0 && v < 3600 {
				m.target = time.Duration(v * float64(time.Second))
			}
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			m.sequence, _ = strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64)
		case line == "#EXT-X-DISCONTINUITY":
			pendingDisc = true
		case line == "#EXT-X-GAP":
			pendingGap = true
		case line == "#EXT-X-ENDLIST":
			m.ended = true
		case strings.HasPrefix(line, "#EXTINF:"):
			v, _, _ := strings.Cut(strings.TrimPrefix(line, "#EXTINF:"), ",")
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && f >= 0 && f < 3600 {
				dur = time.Duration(f * float64(time.Second))
			}
			haveInf = true
		case strings.HasPrefix(line, "#EXT-X-BYTERANGE:"):
			n, off, has := parseByteRange(strings.TrimPrefix(line, "#EXT-X-BYTERANGE:"))
			if !has {
				off = nextOff
			}
			cur.rangeLen, cur.rangeOff = n, off
			nextOff = off + n
		case strings.HasPrefix(line, "#EXT-X-KEY:"):
			a := hlsAttrs(strings.TrimPrefix(line, "#EXT-X-KEY:"))
			key = hlsKey{method: strings.ToUpper(a["METHOD"]), iv: a["IV"], format: a["KEYFORMAT"], versions: a["KEYFORMATVERSIONS"]}
			if key.method == "NONE" {
				key = hlsKey{}
			} else if u, ok := resolveHLS(base, a["URI"]); ok {
				key.uri = u
			} else {
				return m, false
			}
		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			a := hlsAttrs(strings.TrimPrefix(line, "#EXT-X-MAP:"))
			u, ok := resolveHLS(base, a["URI"])
			if !ok {
				return m, false
			}
			mapURI = u
			mapLen, mapOff = 0, 0
			if r := a["BYTERANGE"]; r != "" {
				mapLen, mapOff, _ = parseByteRange(r)
			}
		case strings.HasPrefix(line, "#"):
		default:
			if !haveInf {
				continue
			}
			u, ok := resolveHLS(base, line)
			if !ok {
				return m, false
			}
			cur.uri, cur.dur, cur.disc, cur.gap = u, dur, pendingDisc, pendingGap
			cur.key, cur.mapURI, cur.mapLen, cur.mapOff = key, mapURI, mapLen, mapOff
			cur.msn = m.sequence + int64(len(m.segments))
			if cur.rangeLen == 0 {
				nextOff = 0
			}
			m.segments = append(m.segments, cur)
			cur = playlistSegment{}
			pendingDisc, pendingGap, haveInf, dur = false, false, false, 0
		}
	}
	if m.target <= 0 {
		for _, s := range m.segments {
			m.target = max(m.target, s.dur)
		}
	}
	return m, true
}

func parseMasterPlaylist(body []byte, base *url.URL) masterPlaylist {
	out := masterPlaylist{demuxedAudio: map[string]bool{}}
	var pending *hlsVariant
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
			a := hlsAttrs(strings.TrimPrefix(line, "#EXT-X-STREAM-INF:"))
			v := hlsVariant{codecs: strings.ToLower(a["CODECS"]), audio: a["AUDIO"]}
			v.bandwidth, _ = strconv.Atoi(a["BANDWIDTH"])
			if w, h, ok := strings.Cut(a["RESOLUTION"], "x"); ok {
				v.width, _ = strconv.Atoi(w)
				v.height, _ = strconv.Atoi(h)
			}
			pending = &v
		case strings.HasPrefix(line, "#EXT-X-MEDIA:"):
			a := hlsAttrs(strings.TrimPrefix(line, "#EXT-X-MEDIA:"))
			if strings.EqualFold(a["TYPE"], "AUDIO") && a["URI"] != "" {
				out.demuxedAudio[a["GROUP-ID"]] = true
			}
		case line == "" || strings.HasPrefix(line, "#"):
		default:
			if pending == nil {
				continue
			}
			if u, ok := resolveHLS(base, line); ok {
				pending.uri = u
				out.variants = append(out.variants, *pending)
			}
			pending = nil
		}
	}
	return out
}

// variantClass is the part of a viewer's capabilities that decides which
// provider variant fits. Viewers in the same class share one timeline.
func variantClass(caps core.ClientCaps) string {
	h := caps.MaxHeight
	if h <= 0 || h >= 2160 {
		h = 0
	}
	kbps := caps.MaxBitrateKbps
	if kbps > 0 {
		kbps = 1 << int(math.Ceil(math.Log2(float64(kbps)))) // coarse buckets
	}
	hevc := caps.SupportsVideo("hevc") || len(caps.VideoCodecs) == 0
	return strconv.Itoa(h) + "/" + strconv.Itoa(kbps) + "/" + strconv.FormatBool(hevc)
}

// pickVariant keeps the best muxed variant the viewer can play: codecs it
// declares, within its height and bitrate ceilings. Variants whose audio
// lives in a separate rendition cannot ride one owned timeline; ok=false
// tells the caller to use the relay, which remuxes them.
func pickVariant(m masterPlaylist, caps core.ClientCaps) (hlsVariant, bool) {
	playable := func(v hlsVariant) bool {
		if v.audio != "" && m.demuxedAudio[v.audio] {
			return false
		}
		if len(caps.VideoCodecs) > 0 && v.codecs != "" {
			for _, c := range strings.Split(v.codecs, ",") {
				c = strings.TrimSpace(c)
				switch {
				case strings.HasPrefix(c, "hvc1"), strings.HasPrefix(c, "hev1"):
					if !caps.SupportsVideo("hevc") {
						return false
					}
				case strings.HasPrefix(c, "av01"):
					if !caps.SupportsVideo("av1") {
						return false
					}
				}
			}
		}
		return true
	}
	fits := func(v hlsVariant) bool {
		if caps.MaxHeight > 0 && v.height > caps.MaxHeight {
			return false
		}
		if caps.MaxBitrateKbps > 0 && v.bandwidth > caps.MaxBitrateKbps*1000 {
			return false
		}
		return true
	}
	var candidates []hlsVariant
	for _, v := range m.variants {
		if playable(v) {
			candidates = append(candidates, v)
		}
	}
	if len(candidates) == 0 {
		return hlsVariant{}, false
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].bandwidth > candidates[j].bandwidth })
	for _, v := range candidates {
		if fits(v) {
			return v, true
		}
	}
	// Nothing fits the ceiling: the smallest is the closest.
	return candidates[len(candidates)-1], true
}

// sameVariant finds the variant matching a previous choice after a playlist
// refresh or source re-resolution (URLs carry rotating tokens).
func sameVariant(m masterPlaylist, want hlsVariant, caps core.ClientCaps) (hlsVariant, bool) {
	for _, v := range m.variants {
		if v.bandwidth == want.bandwidth && v.height == want.height && v.codecs == want.codecs {
			return v, true
		}
	}
	return pickVariant(m, caps)
}
