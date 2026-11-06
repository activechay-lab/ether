package hlsplay

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
)

type entry struct {
	index    int
	duration float64
	url      string
}
type playlist struct {
	variant string
	entries []entry
	end     bool
}

func resolve(base, ref string) (string, error) {
	b, e := url.Parse(base)
	if e != nil {
		return "", fmt.Errorf("invalid base URL")
	}
	r, e := url.Parse(ref)
	if e != nil {
		return "", fmt.Errorf("invalid authored URL")
	}
	u := b.ResolveReference(r)
	if u.Host != b.Host || u.Scheme != b.Scheme || u.User != nil || u.Fragment != "" {
		return "", fmt.Errorf("cross-origin media URL refused")
	}
	return u.String(), nil
}
func parsePlaylist(base string, data []byte) (playlist, error) {
	p := playlist{}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "#EXTM3U" {
		return p, fmt.Errorf("invalid HLS playlist")
	}
	index := 0
	duration := 0.0
	variant := false
	for _, raw := range lines[1:] {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
			variant = true
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			n, e := strconv.Atoi(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"))
			if e != nil || n < 0 {
				return p, fmt.Errorf("invalid sequence")
			}
			index = n
		case strings.HasPrefix(line, "#EXTINF:"):
			d, e := strconv.ParseFloat(strings.Split(strings.TrimPrefix(line, "#EXTINF:"), ",")[0], 64)
			if e != nil || math.IsNaN(d) || math.IsInf(d, 0) || d <= 0 || d > 60 {
				return p, fmt.Errorf("invalid duration")
			}
			duration = d
		case line == "#EXT-X-ENDLIST":
			p.end = true
		case strings.HasPrefix(line, "#EXT-X-KEY:"), strings.HasPrefix(line, "#EXT-X-MAP:"), strings.HasPrefix(line, "#EXT-X-BYTERANGE:"), line == "#EXT-X-DISCONTINUITY":
			return p, fmt.Errorf("requires clear continuous MPEG-TS HLS")
		case line != "" && !strings.HasPrefix(line, "#"):
			u, e := resolve(base, line)
			if e != nil {
				return p, e
			}
			if variant {
				p.variant = u
				return p, nil
			}
			if duration <= 0 {
				return p, fmt.Errorf("segment without duration")
			}
			p.entries = append(p.entries, entry{index, duration, u})
			index++
			duration = 0
		}
	}
	if duration != 0 {
		return p, fmt.Errorf("missing segment URI")
	}
	return p, nil
}
