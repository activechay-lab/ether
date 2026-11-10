// Package scrape fetches pages through Ether's host HTTP service (so
// the manifest network allowlist applies) and pulls text and links out
// of simple HTML. It rate-limits per host to stay polite. For anything
// trickier than listing pages, parse the body in plugin code.
package scrape

import (
	"context"
	"html"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	etherplugin "github.com/braedonsaunders/ether/sdk/go"
)

// Fetcher wraps a plugin Host with a polite per-host delay.
type Fetcher struct {
	Host   etherplugin.Host
	Delay  time.Duration // between requests to one host; default 250ms
	mu     sync.Mutex
	lastAt map[string]time.Time
}

// Get fetches a URL through the host and returns the body.
func (f *Fetcher) Get(ctx context.Context, rawURL string) (string, int, error) {
	if err := f.wait(ctx, rawURL); err != nil {
		return "", 0, err
	}
	resp, err := f.Host.Fetch(ctx, etherplugin.HostFetchRequest{Method: "GET", URL: rawURL})
	return resp.Body, resp.Status, err
}

func (f *Fetcher) wait(ctx context.Context, rawURL string) error {
	delay := f.Delay
	if delay <= 0 {
		delay = 250 * time.Millisecond
	}
	host := rawURL
	if u, err := url.Parse(rawURL); err == nil {
		host = u.Host
	}
	f.mu.Lock()
	if f.lastAt == nil {
		f.lastAt = map[string]time.Time{}
	}
	next := f.lastAt[host].Add(delay)
	f.lastAt[host] = time.Now()
	f.mu.Unlock()
	if d := time.Until(next); d > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
	f.mu.Lock()
	f.lastAt[host] = time.Now()
	f.mu.Unlock()
	return nil
}

var (
	tagRe  = regexp.MustCompile(`(?s)<script.*?</script>|<style.*?</style>|<[^>]+>`)
	linkRe = regexp.MustCompile(`(?is)<a\s[^>]*href\s*=\s*["']([^"']+)["'][^>]*>(.*?)</a>`)
)

// Text strips markup down to readable text.
func Text(pageHTML string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(pageHTML, " "))), " ")
}

// Link is one anchor: absolute URL plus its visible text.
type Link struct {
	URL  string
	Text string
}

// Links extracts anchors, resolving relative hrefs against base.
func Links(pageHTML, base string) []Link {
	baseURL, _ := url.Parse(base)
	var out []Link
	for _, m := range linkRe.FindAllStringSubmatch(pageHTML, -1) {
		href := html.UnescapeString(strings.TrimSpace(m[1]))
		if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "javascript:") {
			continue
		}
		if baseURL != nil {
			if u, err := url.Parse(href); err == nil {
				href = baseURL.ResolveReference(u).String()
			}
		}
		out = append(out, Link{URL: href, Text: Text(m[2])})
	}
	return out
}

// Matches returns the capturing groups of every regex match — the
// escape hatch for listing pages with regular shapes.
func Matches(page string, re *regexp.Regexp) [][]string {
	return re.FindAllStringSubmatch(page, -1)
}
