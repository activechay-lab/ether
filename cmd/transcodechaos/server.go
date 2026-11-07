package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var mapURI = regexp.MustCompile(`URI="([^"]+)"`)

type labClient struct {
	base     *url.URL
	token    string
	clientID string
	profile  string
	client   *http.Client
}

func newLabClient(ctx context.Context, raw, token string) (*labClient, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid lab server URL")
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if err != nil {
		return nil, fmt.Errorf("lab host could not be resolved")
	}
	// ETHER_PROTECTED_ADDRS lists hosts (e.g. production) that are never
	// chaos targets.
	for _, addr := range ips {
		for _, p := range strings.Fields(os.Getenv("ETHER_PROTECTED_ADDRS")) {
			if addr.IP.Equal(net.ParseIP(p)) {
				return nil, fmt.Errorf("protected server is not a chaos target")
			}
		}
	}
	u.Path = ""
	u.RawPath = ""
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	l := &labClient{base: u, token: token, clientID: "ether-chaos-" + hex.EncodeToString(nonce), client: &http.Client{Timeout: 60 * time.Second}}
	l.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != u.Scheme || req.URL.Host != u.Host {
			return fmt.Errorf("cross-origin redirect refused")
		}
		if len(via) > 5 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	}
	// This Ether-specific read-only endpoint guards against accidentally driving
	// a real Plex Media Server. No credentials are included in the URL.
	body, _, err := l.get(ctx, "/ether/api/health")
	if err != nil {
		return nil, fmt.Errorf("Ether health check failed: %w", err)
	}
	var h struct {
		OK    bool  `json:"ok"`
		Plex  *bool `json:"plexEnabled"`
		Setup *bool `json:"setupComplete"`
	}
	if json.Unmarshal(body, &h) != nil || !h.OK || h.Plex == nil || h.Setup == nil {
		return nil, fmt.Errorf("target did not identify as an Ether server")
	}
	return l, nil
}
func (l *labClient) get(ctx context.Context, path string) ([]byte, int, error) {
	return l.request(ctx, http.MethodGet, path)
}
func (l *labClient) request(ctx context.Context, method, path string) ([]byte, int, error) {
	ref, err := url.Parse(path)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid server path")
	}
	u := l.base.ResolveReference(ref)
	if u.Scheme != l.base.Scheme || u.Host != l.base.Host || u.User != nil {
		return nil, 0, fmt.Errorf("cross-origin media URL refused")
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("request construction failed")
	}
	if l.token != "" {
		req.Header.Set("X-Plex-Token", l.token)
	}
	req.Header.Set("X-Plex-Client-Identifier", l.clientID)
	req.Header.Set("X-Plex-Product", "Plex Web")
	req.Header.Set("X-Plex-Platform", "Chrome")
	req.Header.Set("Accept", "application/json")
	if l.profile != "" {
		req.Header.Set("X-Plex-Client-Profile-Extra", l.profile)
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request failed")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 128<<20+1))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("response read failed")
	}
	if len(data) > 128<<20 {
		return nil, resp.StatusCode, fmt.Errorf("response exceeds 128 MiB")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return data, resp.StatusCode, nil
}
func runLabServer(ctx context.Context, raw, key, token, ff, fp, root, mode string) ([]result, error) {
	if strings.Contains(key, ",") {
		var results []result
		for i, k := range strings.Split(key, ",") {
			items, err := runLabServer(ctx, raw, strings.TrimSpace(k), token, ff, fp, root, mode)
			if err != nil {
				return results, err
			}
			for j := range items {
				items[j].Scenario = fmt.Sprintf("staged-title-%d", i+1)
			}
			results = append(results, items...)
		}
		return results, nil
	}
	if token == "" {
		return nil, fmt.Errorf("set ETHER_CHAOS_TOKEN for the lab server")
	}
	if n, err := strconv.ParseUint(key, 10, 64); err != nil || n == 0 {
		return nil, fmt.Errorf("-media-key must be a positive Ether rating key")
	}
	lab, err := newLabClient(ctx, raw, token)
	if err != nil {
		return nil, err
	}
	paths := []string{"hls"}
	if mode == "full" {
		paths = append(paths, "hls-fmp4", "dash", "start-mkv", "download")
	}
	results := make([]result, 0, len(paths))
	for _, path := range paths {
		lab.profile = ""
		if path == "hls-fmp4" {
			lab.profile = "add-transcode-target(type=videoProfile&context=streaming&protocol=hls&container=mp4&videoCodec=h264,hevc&audioCodec=aac)"
		}
		start := time.Now()
		idBytes := make([]byte, 12)
		if _, err := rand.Read(idBytes); err != nil {
			return nil, err
		}
		id := "chaos-" + hex.EncodeToString(idBytes)
		q := url.Values{"path": {"/library/metadata/" + key}, "session": {id}, "directPlay": {"0"}, "directStream": {"1"}}
		detail := func() string {
			defer func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, _, _ = lab.get(cleanup, "/video/:/transcode/universal/stop?session="+id)
			}()
			check := func(data []byte, ext string) error {
				if len(data) == 0 {
					return fmt.Errorf("empty media")
				}
				file := filepath.Join(root, "lab-"+path+"."+ext)
				if err := os.WriteFile(file, data, 0600); err != nil {
					return err
				}
				if err := command(ctx, fp, "-v", "error", "-show_streams", file); err != nil {
					return fmt.Errorf("ffprobe rejected output")
				}
				if err := command(ctx, ff, "-v", "error", "-xerror", "-i", file, "-f", "null", "-"); err != nil {
					return fmt.Errorf("output did not decode")
				}
				return nil
			}
			switch path {
			case "hls", "hls-fmp4":
				q.Set("protocol", "hls")
				if _, _, err := lab.get(ctx, "/video/:/transcode/universal/start.m3u8?"+q.Encode()); err != nil {
					return "start: " + err.Error()
				}
				playlist, _, err := lab.get(ctx, "/video/:/transcode/universal/session/"+id+"/base/index.m3u8")
				if err != nil {
					return "playlist: " + err.Error()
				}
				var init []byte
				var media []string
				for _, line := range strings.Split(string(playlist), "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "#EXT-X-MAP:") {
						if matches := mapURI.FindStringSubmatch(line); len(matches) == 2 {
							u, e := url.Parse(matches[1])
							if e != nil {
								return "bad init URL"
							}
							base, _ := url.Parse("/video/:/transcode/universal/session/" + id + "/base/index.m3u8")
							init, _, e = lab.get(ctx, base.ResolveReference(u).String())
							if e != nil {
								return "init: " + e.Error()
							}
						}
					} else if line != "" && !strings.HasPrefix(line, "#") {
						media = append(media, line)
					}
				}
				if len(media) == 0 {
					return "playlist contains no media"
				}
				base, _ := url.Parse("/video/:/transcode/universal/session/" + id + "/base/index.m3u8")
				order := []int{0}
				if len(media) > 2 {
					order = append(order, 2, 1)
				}
				for _, n := range order {
					u, e := url.Parse(media[n])
					if e != nil {
						return "bad segment URL"
					}
					part, _, e := lab.get(ctx, base.ResolveReference(u).String())
					if e != nil {
						return "segment: " + e.Error()
					}
					ext := "ts"
					if len(init) > 0 {
						ext = "mp4"
					}
					if e = check(append(bytes.Clone(init), part...), ext); e != nil {
						return e.Error()
					}
				}
			case "dash":
				q.Set("protocol", "dash")
				if _, _, err := lab.get(ctx, "/video/:/transcode/universal/start.mpd?"+q.Encode()); err != nil {
					return "manifest: " + err.Error()
				}
				base := "/video/:/transcode/universal/session/" + id + "/0/"
				init, _, err := lab.get(ctx, base+"header")
				if err != nil {
					return "init: " + err.Error()
				}
				part, _, err := lab.get(ctx, base+"0.m4s")
				if err != nil {
					return "media: " + err.Error()
				}
				if err = check(append(init, part...), "mp4"); err != nil {
					return err.Error()
				}
			case "download":
				if err := lab.download(ctx, key, check); err != nil {
					return err.Error()
				}
			case "start-mkv":
				q.Set("protocol", "http")
				data, _, err := lab.get(ctx, "/video/:/transcode/universal/start.mkv?"+q.Encode())
				if err != nil {
					return "stream: " + err.Error()
				}
				if err = check(data, "mkv"); err != nil {
					return err.Error()
				}
			}
			return ""
		}()
		status := "PASS"
		if detail != "" {
			status = "FAIL"
		}
		item := result{"staged-title", "lab/" + path, status, detail, time.Since(start).Milliseconds()}
		results = append(results, item)
		fmt.Printf("%-32s %-20s %-4s %6dms %s\n", item.Scenario, item.Path, item.Status, item.Milliseconds, item.Detail)
	}
	return results, nil
}
